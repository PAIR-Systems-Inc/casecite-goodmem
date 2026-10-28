import { create } from 'zustand';
import { api } from '../api';
import { useUIStore } from './uiStore';
import { useMattersStore } from './mattersStore';
import { registerReset } from './resetRegistry';
import { createJobPoller, toast } from './storeUtils';
import { appNavigate } from '../utils/router';
import logger from '../utils/logger';
import type { RagDocument } from '../types';
import type {
  ReviewCell,
  ReviewFieldSpec,
  ReviewTableDetail,
  ReviewTableEstimate,
  ReviewTableRunResult,
  ReviewTableSummary,
} from '../api/types';

/** A starting schema for a contract sweep; the reviewer edits it freely. */
export const STANDARD_CONTRACT_FIELDS: ReviewFieldSpec[] = [
  { name: 'Counterparty', description: 'The other party to the agreement', type: 'text' },
  { name: 'Effective date', description: 'When the agreement takes effect', type: 'date' },
  { name: 'Term', description: 'Initial term length', type: 'duration' },
  { name: 'Auto-renewal', description: 'Does the agreement renew automatically?', type: 'boolean' },
  { name: 'Termination notice', description: 'Notice required to terminate for convenience', type: 'duration' },
  { name: 'Governing law', description: 'State or country whose law governs', type: 'text' },
  { name: 'Liability cap', description: 'Cap amount or formula', type: 'money' },
  { name: 'Assignment', description: 'Is assignment permitted without consent?', type: 'boolean' },
];

export interface CellRef {
  documentId: string;
  fieldKey: string;
}

export interface RunProgress {
  message: string;
  fraction: number | null;
}

const errorMessage = (e: unknown, fallback: string) => (e instanceof Error && e.message ? e.message : fallback);

/** Cells keyed "documentId\u0000fieldKey" for O(1) grid lookups. */
export function indexCells(cells: ReviewCell[]): Map<string, ReviewCell> {
  const map = new Map<string, ReviewCell>();
  for (const c of cells) map.set(cellKey(c.document_id, c.field_key), c);
  return map;
}

export const cellKey = (documentId: string, fieldKey: string) => `${documentId}\u0000${fieldKey}`;

/** One line summarising a finished run for the toast. */
export function describeRun(result: ReviewTableRunResult | null | undefined): string {
  if (!result) return 'Review finished.';
  const parts = [`${result.cells} cell${result.cells === 1 ? '' : 's'}`];
  if (result.extracted) parts.push(`${result.extracted} verified`);
  if (result.unverified) parts.push(`${result.unverified} unverified`);
  if (result.not_found) parts.push(`${result.not_found} not found`);
  if (result.errors) parts.push(`${result.errors} failed`);
  return `Review finished: ${parts.join(', ')}.`;
}

export interface ReviewTablesState {
  _initialized: boolean;
  tables: ReviewTableSummary[];
  tablesLoading: boolean;
  init: () => void;
  loadTables: () => Promise<void>;
  createTable: (input: {
    name: string;
    description: string;
    fields: ReviewFieldSpec[];
    documentIds: string[];
  }) => Promise<ReviewTableDetail | null>;
  openTable: (tableId: string) => void;

  // ── One table ──
  tableId: string | null;
  table: ReviewTableDetail | null;
  tableLoading: boolean;
  tableError: string | null;
  loadTable: (tableId: string) => Promise<void>;
  estimate: ReviewTableEstimate | null;
  loadEstimate: () => Promise<void>;
  saveDetails: (name: string, description: string) => Promise<boolean>;
  saveFields: (fields: ReviewFieldSpec[]) => Promise<boolean>;
  saveDocuments: (documentIds: string[]) => Promise<boolean>;
  deleteTable: () => void;

  // ── Runs ──
  running: boolean;
  progress: RunProgress | null;
  runError: string | null;
  run: (scope?: { documentIds?: string[]; fieldKeys?: string[] }) => Promise<void>;

  // ── Export ──
  exporting: boolean;
  exportTable: (format: 'xlsx' | 'csv') => Promise<void>;

  // ── Grid UI state shared across components ──
  selectedCell: CellRef | null;
  selectCell: (ref: CellRef | null) => void;
  schemaOpen: boolean;
  setSchemaOpen: (open: boolean) => void;
  pickerOpen: boolean;
  pickerDocs: RagDocument[];
  pickerLoading: boolean;
  openPicker: () => Promise<void>;
  closePicker: () => void;
}

const jobPoller = createJobPoller();

export const useReviewTablesStore = create<ReviewTablesState>((set, get) => ({
  _initialized: false,
  tables: [],
  tablesLoading: false,

  init: () => {
    if (get()._initialized) return;
    set({ _initialized: true });
    void get().loadTables();
  },

  loadTables: async () => {
    set({ tablesLoading: true });
    try {
      const { tables } = await api.listReviewTables();
      set({ tables });
    } catch (e) {
      logger.error('loadReviewTables failed', e);
      toast(errorMessage(e, 'Could not load review tables.'), 'error');
    } finally {
      set({ tablesLoading: false });
    }
  },

  createTable: async ({ name, description, fields, documentIds }) => {
    const trimmed = name.trim();
    if (!trimmed) {
      toast('Give the review a name.', 'error');
      return null;
    }
    const cleanFields = fields.filter((f) => f.name.trim());
    if (cleanFields.length === 0) {
      toast('Add at least one field to extract.', 'error');
      return null;
    }
    try {
      // With an active matter and no explicit documents, the server scopes
      // the table to every document filed under that matter.
      const matterId = useMattersStore.getState().activeMatterId;
      const table = await api.createReviewTable({
        name: trimmed,
        description: description.trim() || null,
        matter_id: matterId,
        fields: cleanFields.map((f) => ({ name: f.name.trim(), description: (f.description || '').trim(), type: f.type })),
        document_ids: documentIds,
      });
      set((st) => ({ tables: [tableSummary(table), ...st.tables] }));
      toast(`Created "${table.name}".`, 'success');
      return table;
    } catch (e) {
      logger.error('createReviewTable failed', e);
      toast(errorMessage(e, 'Could not create the review table.'), 'error');
      return null;
    }
  },

  openTable: (tableId) => {
    appNavigate(`/review-tables/${encodeURIComponent(tableId)}`);
  },

  // ── One table ──
  tableId: null,
  table: null,
  tableLoading: false,
  tableError: null,

  loadTable: async (tableId) => {
    if (get().tableId !== tableId) {
      jobPoller.stop();
      set({
        tableId, table: null, tableError: null, estimate: null, selectedCell: null,
        running: false, progress: null, runError: null, schemaOpen: false, pickerOpen: false,
      });
    }
    set({ tableLoading: true });
    try {
      const table = await api.getReviewTable(tableId);
      if (get().tableId !== tableId) return;
      set({ table, tableError: null, schemaOpen: table.fields.length === 0 });
      void get().loadEstimate();
    } catch (e) {
      logger.error('loadReviewTable failed', e);
      if (get().tableId === tableId) set({ tableError: errorMessage(e, 'Could not load this review table.') });
    } finally {
      if (get().tableId === tableId) set({ tableLoading: false });
    }
  },

  estimate: null,
  loadEstimate: async () => {
    const id = get().tableId;
    if (!id) return;
    try {
      const estimate = await api.estimateReviewTable(id);
      if (get().tableId === id) set({ estimate });
    } catch (e) {
      logger.debug('estimateReviewTable failed', e);
    }
  },

  saveDetails: async (name, description) => {
    const id = get().tableId;
    if (!id || !name.trim()) return false;
    return applyUpdate(id, { name: name.trim(), description: description.trim() || null }, 'Saved.');
  },

  saveFields: async (fields) => {
    const id = get().tableId;
    if (!id) return false;
    const clean = fields
      .filter((f) => f.name.trim())
      .map((f) => ({ key: f.key || undefined, name: f.name.trim(), description: (f.description || '').trim(), type: f.type }));
    if (clean.length === 0) {
      toast('Keep at least one field.', 'error');
      return false;
    }
    const current = get().table;
    const dropped = current ? current.fields.filter((f) => f.key && !clean.some((c) => c.key === f.key)) : [];
    const hasCells = dropped.some((f) => current!.cells.some((c) => c.field_key === f.key));
    if (hasCells) {
      return new Promise<boolean>((resolve) => {
        useUIStore.getState().showConfirm({
          title: 'Remove extracted columns?',
          message: `Removing ${dropped.map((f) => `"${f.name}"`).join(', ')} also deletes the values already extracted for ${dropped.length === 1 ? 'it' : 'them'}.`,
          type: 'danger',
          confirmText: 'Remove',
          onConfirm: async () => resolve(await applyUpdate(id, { fields: clean }, 'Schema saved.')),
          onCancel: () => resolve(false),
        });
      });
    }
    return applyUpdate(id, { fields: clean }, 'Schema saved.');
  },

  saveDocuments: async (documentIds) => {
    const id = get().tableId;
    if (!id) return false;
    const ok = await applyUpdate(id, { document_ids: documentIds }, `${documentIds.length} document${documentIds.length === 1 ? '' : 's'} in scope.`);
    if (ok) set({ pickerOpen: false });
    return ok;
  },

  deleteTable: () => {
    const table = get().table;
    if (!table) return;
    useUIStore.getState().showConfirm({
      title: `Delete "${table.name}"?`,
      message: 'The schema and every extracted value and quote are permanently deleted. The documents themselves are not affected.',
      type: 'danger',
      confirmText: 'Delete review',
      onConfirm: async () => {
        try {
          await api.deleteReviewTable(table.id);
          jobPoller.stop();
          set((st) => ({
            tables: st.tables.filter((t) => t.id !== table.id),
            tableId: null, table: null, estimate: null, running: false, progress: null,
          }));
          toast('Review table deleted.', 'success');
          appNavigate('/review-tables');
        } catch (e) {
          logger.error('deleteReviewTable failed', e);
          toast(errorMessage(e, 'Could not delete the review table.'), 'error');
        }
      },
    });
  },

  // ── Runs ──
  running: false,
  progress: null,
  runError: null,

  run: async (scope = {}) => {
    const id = get().tableId;
    const table = get().table;
    if (!id || !table || get().running) return;
    if (table.documents.length === 0 || table.fields.length === 0) {
      toast('Add at least one document and one field before running.', 'error');
      return;
    }
    jobPoller.stop();
    set({ running: true, runError: null, progress: { message: 'Starting…', fraction: 0 } });
    try {
      const started = await api.runReviewTable(id, {
        document_ids: scope.documentIds,
        field_keys: scope.fieldKeys,
      });
      jobPoller.start({
        fetchJob: () => api.getReviewTableJob(started.job_id),
        maxAttempts: 600,
        intervalMs: 2000,
        failedMessage: 'The review run failed.',
        timeoutMessage: 'Timed out waiting for the review to finish.',
        onPending: (job) => {
          if (get().tableId !== id) return;
          if (job.progress) set({ progress: { message: job.progress.message, fraction: job.progress.fraction } });
        },
        onCompleted: async (job) => {
          if (get().tableId !== id) return;
          set({ running: false, progress: null });
          toast(describeRun(job.result), job.result?.errors ? 'warning' : 'success');
          await get().loadTable(id);
          void get().loadTables();
        },
        onFailed: (message) => {
          if (get().tableId !== id) return;
          set({ running: false, progress: null, runError: message });
          toast(message, 'error');
          void get().loadTable(id); // whatever finished before the failure is already stored
        },
      });
    } catch (e) {
      logger.error('runReviewTable failed', e);
      const message = errorMessage(e, 'Could not start the review.');
      set({ running: false, progress: null, runError: message });
      toast(message, 'error');
    }
  },

  // ── Export ──
  exporting: false,
  exportTable: async (format) => {
    const table = get().table;
    if (!table || get().exporting) return;
    set({ exporting: true });
    try {
      const blob = await api.exportReviewTable(table.id, format);
      const url = URL.createObjectURL(blob);
      try {
        const a = document.createElement('a');
        a.href = url;
        a.download = `${table.name.replace(/[^A-Za-z0-9._-]+/g, '-').replace(/^-+|-+$/g, '') || 'review'}.${format}`;
        document.body.appendChild(a);
        a.click();
        a.remove();
      } finally {
        // Give the browser a tick to start the download before the URL goes away.
        setTimeout(() => URL.revokeObjectURL(url), 1000);
      }
      toast(`Exported ${format.toUpperCase()}.`, 'success');
    } catch (e) {
      logger.error('exportReviewTable failed', e);
      toast(errorMessage(e, 'Export failed.'), 'error');
    } finally {
      set({ exporting: false });
    }
  },

  // ── Grid UI state ──
  selectedCell: null,
  selectCell: (ref) => set({ selectedCell: ref }),
  schemaOpen: false,
  setSchemaOpen: (open) => set({ schemaOpen: open }),
  pickerOpen: false,
  pickerDocs: [],
  pickerLoading: false,

  openPicker: async () => {
    set({ pickerOpen: true, pickerLoading: true });
    try {
      // Within an active matter the picker offers that matter's documents
      // (including colleagues' filed ones); otherwise everything the user can read.
      const matterId = useMattersStore.getState().activeMatterId;
      const docs = matterId
        ? (await api.getMatterDocuments(matterId)).documents
        : await api.getAllDocuments();
      set({ pickerDocs: docs as RagDocument[] });
    } catch (e) {
      logger.error('review picker load failed', e);
      toast(errorMessage(e, 'Could not load your documents.'), 'error');
      set({ pickerDocs: [] });
    } finally {
      set({ pickerLoading: false });
    }
  },
  closePicker: () => set({ pickerOpen: false }),
}));

function tableSummary(table: ReviewTableDetail): ReviewTableSummary {
  const { fields: _fields, documents: _documents, cells: _cells, ...summary } = table;
  return summary;
}

/** PATCH the open table, replace it in the store, refresh the estimate. */
async function applyUpdate(
  id: string,
  changes: Parameters<typeof api.updateReviewTable>[1],
  successMessage: string,
): Promise<boolean> {
  try {
    const table = await api.updateReviewTable(id, changes);
    const st = useReviewTablesStore.getState();
    if (st.tableId !== id) return true;
    useReviewTablesStore.setState((prev) => ({
      table,
      selectedCell: null,
      tables: prev.tables.map((t) => (t.id === id ? tableSummary(table) : t)),
    }));
    void st.loadEstimate();
    toast(successMessage, 'success');
    return true;
  } catch (e) {
    logger.error('updateReviewTable failed', e);
    toast(errorMessage(e, 'Could not save the change.'), 'error');
    return false;
  }
}

registerReset(() => {
  jobPoller.stop();
  useReviewTablesStore.setState(useReviewTablesStore.getInitialState(), true);
});
