import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

vi.mock('../api', () => ({
  api: {
    listReviewTables: vi.fn(),
    createReviewTable: vi.fn(),
    getReviewTable: vi.fn(),
    updateReviewTable: vi.fn(),
    deleteReviewTable: vi.fn(),
    estimateReviewTable: vi.fn(),
    runReviewTable: vi.fn(),
    getReviewTableJob: vi.fn(),
    exportReviewTable: vi.fn(),
    getMatterDocuments: vi.fn(),
    getAllDocuments: vi.fn(),
    listMatters: vi.fn().mockResolvedValue({ matters: [] }),
  },
  API_BASE_URL: 'http://localhost:8000/api/v1',
}));

vi.mock('../utils/logger', () => ({ default: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() } }));

const navigate = vi.fn();
vi.mock('../utils/router', () => ({ appNavigate: (to: string) => navigate(to) }));

import { useReviewTablesStore, indexCells, cellKey, describeRun } from './reviewTablesStore';
import { useMattersStore } from './mattersStore';
import { useUIStore } from './uiStore';
import { api } from '../api';

const summary = {
  id: 't1', name: 'NDA sweep', description: null, matter_id: null, owner_id: 'me', role: 'owner',
  field_count: 2, document_count: 1, cell_count: 0, last_run_at: null,
  created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z',
};
const fields = [
  { key: 'governing_law', name: 'Governing law', description: '', type: 'text' as const },
  { key: 'term', name: 'Term', description: '', type: 'duration' as const },
];
const detail = {
  ...summary,
  fields,
  documents: [{ id: 'd1', filename: 'a.pdf', accessible: true }],
  cells: [
    {
      document_id: 'd1', field_key: 'governing_law', value: 'Delaware', quote: 'laws of Delaware',
      span_start: 10, span_end: 26, verified: true, status: 'extracted', error: null,
      model_used: 'gpt', updated_at: '2026-10-02T00:00:00Z',
    },
  ],
};
const estimate = { documents: 1, fields: 2, cells: 2, llm_calls: 1, approx_input_tokens: 1200, model: 'gpt' };

/** Settle pending promises and zero-delay timers under the fake clock. */
const flush = () => vi.advanceTimersByTimeAsync(0);

describe('reviewTablesStore', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    useReviewTablesStore.setState(useReviewTablesStore.getInitialState(), true);
    useMattersStore.setState(useMattersStore.getInitialState(), true);
    useUIStore.setState(useUIStore.getInitialState(), true);
    vi.clearAllMocks();
    (api.listReviewTables as any).mockResolvedValue({ tables: [summary] });
    (api.getReviewTable as any).mockResolvedValue(detail);
    (api.estimateReviewTable as any).mockResolvedValue(estimate);
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  // ==================== helpers ====================

  it('indexCells keys cells by document and field', () => {
    const map = indexCells(detail.cells);
    expect(map.get(cellKey('d1', 'governing_law'))?.value).toBe('Delaware');
    expect(map.get(cellKey('d1', 'term'))).toBeUndefined();
  });

  it('describeRun summarises a finished run', () => {
    expect(describeRun(null)).toBe('Review finished.');
    expect(describeRun({ documents: 2, cells: 4, extracted: 2, unverified: 1, errors: 1 })).toBe(
      'Review finished: 4 cells, 2 verified, 1 unverified, 1 failed.',
    );
  });

  // ==================== list / create ====================

  it('init loads the tables once', async () => {
    useReviewTablesStore.getState().init();
    useReviewTablesStore.getState().init();
    await flush();
    expect(api.listReviewTables).toHaveBeenCalledTimes(1);
    expect(useReviewTablesStore.getState().tables).toEqual([summary]);
  });

  it('createTable sends the active matter, trims fields, prepends the summary and returns the table', async () => {
    useMattersStore.setState({ activeMatterId: 'm1' });
    (api.createReviewTable as any).mockResolvedValue(detail);
    const created = await useReviewTablesStore.getState().createTable({
      name: '  NDA sweep ',
      description: '',
      fields: [{ name: ' Governing law ', description: ' state ', type: 'text' }, { name: '   ', type: 'text' }],
      documentIds: [],
    });
    expect(created).toEqual(detail);
    expect(api.createReviewTable).toHaveBeenCalledWith({
      name: 'NDA sweep',
      description: null,
      matter_id: 'm1',
      fields: [{ name: 'Governing law', description: 'state', type: 'text' }],
      document_ids: [],
    });
    expect(useReviewTablesStore.getState().tables[0].id).toBe('t1');
  });

  it('createTable refuses an empty name or schema without calling the API', async () => {
    expect(await useReviewTablesStore.getState().createTable({ name: '', description: '', fields, documentIds: [] })).toBeNull();
    expect(await useReviewTablesStore.getState().createTable({ name: 'x', description: '', fields: [], documentIds: [] })).toBeNull();
    expect(api.createReviewTable).not.toHaveBeenCalled();
    expect(useUIStore.getState().toasts.length).toBe(2);
  });

  it('openTable navigates to the table route', () => {
    useReviewTablesStore.getState().openTable('t1');
    expect(navigate).toHaveBeenCalledWith('/review-tables/t1');
  });

  // ==================== one table ====================

  it('loadTable fetches the detail and the estimate, opening the schema editor when there are no fields', async () => {
    await useReviewTablesStore.getState().loadTable('t1');
    await flush();
    const st = useReviewTablesStore.getState();
    expect(st.table).toEqual(detail);
    expect(st.estimate).toEqual(estimate);
    expect(st.schemaOpen).toBe(false);

    (api.getReviewTable as any).mockResolvedValue({ ...detail, id: 't2', fields: [], cells: [] });
    await useReviewTablesStore.getState().loadTable('t2');
    expect(useReviewTablesStore.getState().schemaOpen).toBe(true);
  });

  it('loadTable records the error for an unreadable table', async () => {
    (api.getReviewTable as any).mockRejectedValue(new Error('Review table not found'));
    await useReviewTablesStore.getState().loadTable('nope');
    expect(useReviewTablesStore.getState().tableError).toBe('Review table not found');
    expect(useReviewTablesStore.getState().table).toBeNull();
  });

  it('saveDocuments PATCHes the scope, replaces the table and closes the picker', async () => {
    await useReviewTablesStore.getState().loadTable('t1');
    useReviewTablesStore.setState({ pickerOpen: true, tables: [summary] });
    const updated = { ...detail, documents: [...detail.documents, { id: 'd2', filename: 'b.pdf', accessible: true }], document_count: 2 };
    (api.updateReviewTable as any).mockResolvedValue(updated);

    expect(await useReviewTablesStore.getState().saveDocuments(['d1', 'd2'])).toBe(true);
    expect(api.updateReviewTable).toHaveBeenCalledWith('t1', { document_ids: ['d1', 'd2'] });
    const st = useReviewTablesStore.getState();
    expect(st.table?.documents).toHaveLength(2);
    expect(st.pickerOpen).toBe(false);
    expect(st.tables.find((t) => t.id === 't1')?.document_count).toBe(2);
  });

  it('saveFields asks before dropping a column that already has values', async () => {
    await useReviewTablesStore.getState().loadTable('t1');
    (api.updateReviewTable as any).mockResolvedValue({ ...detail, fields: [fields[1]], cells: [] });

    const pending = useReviewTablesStore.getState().saveFields([fields[1]]);
    await flush();
    const confirm = useUIStore.getState().confirmModal;
    expect(confirm?.open).toBe(true);
    expect(api.updateReviewTable).not.toHaveBeenCalled();
    confirm?.onConfirm?.();
    expect(await pending).toBe(true);
    expect(api.updateReviewTable).toHaveBeenCalledWith('t1', {
      fields: [{ key: 'term', name: 'Term', description: '', type: 'duration' }],
    });
  });

  it('saveFields saves straight away when no extracted column is dropped', async () => {
    await useReviewTablesStore.getState().loadTable('t1');
    const added = [...fields, { name: 'Renewal', description: '', type: 'boolean' as const }];
    (api.updateReviewTable as any).mockResolvedValue({ ...detail, fields: added });
    expect(await useReviewTablesStore.getState().saveFields(added)).toBe(true);
    expect(useUIStore.getState().confirmModal?.open).not.toBe(true);
    expect(api.updateReviewTable).toHaveBeenCalledTimes(1);
  });

  it('deleteTable confirms, deletes, drops the table from the list and navigates back', async () => {
    await useReviewTablesStore.getState().loadTable('t1');
    useReviewTablesStore.setState({ tables: [summary] });
    (api.deleteReviewTable as any).mockResolvedValue({ status: 'deleted', id: 't1' });

    useReviewTablesStore.getState().deleteTable();
    const confirm = useUIStore.getState().confirmModal;
    expect(confirm?.open).toBe(true);
    await confirm?.onConfirm?.();
    await flush();
    expect(api.deleteReviewTable).toHaveBeenCalledWith('t1');
    expect(useReviewTablesStore.getState().tables).toEqual([]);
    expect(useReviewTablesStore.getState().table).toBeNull();
    expect(navigate).toHaveBeenCalledWith('/review-tables');
  });

  // ==================== runs ====================

  it('run submits the job, tracks progress and reloads the table when it completes', async () => {
    await useReviewTablesStore.getState().loadTable('t1');
    (api.runReviewTable as any).mockResolvedValue({ job_id: 'j1', poll_url: '/api/v1/jobs/j1', documents: 1, fields: 2 });
    (api.getReviewTableJob as any)
      .mockResolvedValueOnce({ job_id: 'j1', status: 'processing', progress: { message: 'Extracted 0/1 documents', fraction: 0 } })
      .mockResolvedValueOnce({ job_id: 'j1', status: 'completed', result: { documents: 1, cells: 2, extracted: 2 } });

    await useReviewTablesStore.getState().run({ documentIds: ['d1'] });
    expect(api.runReviewTable).toHaveBeenCalledWith('t1', { document_ids: ['d1'], field_keys: undefined });
    expect(useReviewTablesStore.getState().running).toBe(true);

    await flush(); // first poll
    expect(useReviewTablesStore.getState().progress?.message).toBe('Extracted 0/1 documents');

    await vi.advanceTimersByTimeAsync(2000); // second poll completes the job
    await flush();
    const st = useReviewTablesStore.getState();
    expect(st.running).toBe(false);
    expect(st.progress).toBeNull();
    expect(api.getReviewTable).toHaveBeenCalledTimes(2);
    const toasts = useUIStore.getState().toasts;
    expect(toasts[toasts.length - 1]?.message).toBe('Review finished: 2 cells, 2 verified.');
  });

  it('run surfaces a failed job and reloads whatever finished', async () => {
    await useReviewTablesStore.getState().loadTable('t1');
    (api.runReviewTable as any).mockResolvedValue({ job_id: 'j1', poll_url: '', documents: 1, fields: 2 });
    (api.getReviewTableJob as any).mockResolvedValue({ job_id: 'j1', status: 'failed', error: 'provider down' });

    await useReviewTablesStore.getState().run();
    await flush();
    const st = useReviewTablesStore.getState();
    expect(st.running).toBe(false);
    expect(st.runError).toBe('provider down');
    expect(api.getReviewTable).toHaveBeenCalledTimes(2);
  });

  it('run refuses a table with nothing to extract and a start failure', async () => {
    (api.getReviewTable as any).mockResolvedValue({ ...detail, documents: [] });
    await useReviewTablesStore.getState().loadTable('t1');
    await useReviewTablesStore.getState().run();
    expect(api.runReviewTable).not.toHaveBeenCalled();

    (api.getReviewTable as any).mockResolvedValue(detail);
    await useReviewTablesStore.getState().loadTable('t1');
    (api.runReviewTable as any).mockRejectedValue(new Error('No key'));
    await useReviewTablesStore.getState().run();
    expect(useReviewTablesStore.getState().runError).toBe('No key');
    expect(useReviewTablesStore.getState().running).toBe(false);
  });

  // ==================== picker ====================

  it('openPicker lists the active matter documents, else everything', async () => {
    (api.getAllDocuments as any).mockResolvedValue([{ id: 'd9', filename: 'z.pdf', status: 'indexed' }]);
    await useReviewTablesStore.getState().openPicker();
    expect(api.getAllDocuments).toHaveBeenCalled();
    expect(useReviewTablesStore.getState().pickerDocs.map((d) => d.id)).toEqual(['d9']);
    expect(useReviewTablesStore.getState().pickerOpen).toBe(true);

    useMattersStore.setState({ activeMatterId: 'm1' });
    (api.getMatterDocuments as any).mockResolvedValue({ documents: [{ id: 'd1', filename: 'a.pdf', status: 'indexed' }] });
    await useReviewTablesStore.getState().openPicker();
    expect(api.getMatterDocuments).toHaveBeenCalledWith('m1');
    expect(useReviewTablesStore.getState().pickerDocs.map((d) => d.id)).toEqual(['d1']);
  });
});
