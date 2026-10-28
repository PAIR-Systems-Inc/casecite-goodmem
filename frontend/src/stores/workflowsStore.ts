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
  WorkflowDetail,
  WorkflowRunDetail,
  WorkflowRunResult,
  WorkflowRunSummary,
  WorkflowStep,
  WorkflowStepKind,
  WorkflowSummary,
  WorkflowTemplate,
} from '../api/types';

/** What each step kind does, for the editor and the run view. */
export const STEP_KINDS: Array<{ kind: WorkflowStepKind; title: string; desc: string; icon: string }> = [
  { kind: 'extract_fields', title: 'Extract fields', desc: 'A tabular review of the input documents against a field schema', icon: 'Table2' },
  { kind: 'analyze_contract', title: 'Analyse contracts', desc: 'The full contract review on each input document', icon: 'FileSearch' },
  { kind: 'compare_contracts', title: 'Compare contracts', desc: 'Clause by clause against a baseline (the first input by default)', icon: 'GitCompare' },
  { kind: 'draft_document', title: 'Draft a document', desc: 'From instructions, with earlier outputs attached as references', icon: 'PenLine' },
  { kind: 'deep_research', title: 'Deep research', desc: 'A planned, source-verified research question over the inputs', icon: 'ScanSearch' },
  { kind: 'citation_check', title: 'Check citations', desc: 'Treatment scan of citations found in earlier outputs', icon: 'ShieldCheck' },
];

export const stepKindInfo = (kind: string) =>
  STEP_KINDS.find((k) => k.kind === kind) || { kind: kind as WorkflowStepKind, title: kind, desc: '', icon: 'Zap' };

const DEFAULT_PARAMS: Record<WorkflowStepKind, Record<string, unknown>> = {
  extract_fields: { fields: [{ name: 'Counterparty', description: '', type: 'text' }] },
  analyze_contract: { contract_type: 'auto', posture: 'balanced' },
  compare_contracts: { focus: '' },
  draft_document: { instructions: '', include_outputs_from: [] },
  deep_research: { question: '' },
  citation_check: { citations: [], from_steps: null },
};

/** A fresh step of `kind` with a name no other step uses. */
export function newStep(kind: WorkflowStepKind, existing: WorkflowStep[]): WorkflowStep {
  const base = kind.split('_')[0];
  const taken = new Set(existing.map((s) => s.name.toLowerCase()));
  let name = base;
  for (let i = 2; taken.has(name); i += 1) name = `${base}${i}`;
  return { name, kind, params: { ...DEFAULT_PARAMS[kind] }, inputs: { source: 'run', document_ids: [], step: null } };
}

export interface RunProgress {
  message: string;
  fraction: number | null;
}

const errorMessage = (e: unknown, fallback: string) => (e instanceof Error && e.message ? e.message : fallback);

/** One line summarising a finished run for the toast. */
export function describeRun(result: WorkflowRunResult | null | undefined): string {
  if (!result) return 'Workflow finished.';
  const produced = Object.entries(result.produced || {})
    .filter(([, n]) => n > 0)
    .map(([k, n]) => `${n} ${k.replace(/_/g, ' ')}`);
  const steps = `${result.completed_steps} step${result.completed_steps === 1 ? '' : 's'}`;
  return produced.length ? `Workflow finished: ${steps}, produced ${produced.join(', ')}.` : `Workflow finished: ${steps}.`;
}

export interface WorkflowsState {
  _initialized: boolean;
  workflows: WorkflowSummary[];
  workflowsLoading: boolean;
  templates: WorkflowTemplate[];
  init: () => void;
  loadWorkflows: () => Promise<void>;
  loadTemplates: () => Promise<void>;
  createWorkflow: (input: { name: string; description: string; steps: WorkflowStep[] }) => Promise<WorkflowDetail | null>;
  createFromTemplate: (key: string) => Promise<WorkflowDetail | null>;
  openWorkflow: (workflowId: string) => void;

  // ── One workflow ──
  workflowId: string | null;
  workflow: WorkflowDetail | null;
  workflowLoading: boolean;
  workflowError: string | null;
  loadWorkflow: (workflowId: string) => Promise<void>;
  saveDetails: (name: string, description: string) => Promise<boolean>;
  saveSteps: (steps: WorkflowStep[]) => Promise<boolean>;
  deleteWorkflow: () => void;

  // ── Runs ──
  runs: WorkflowRunSummary[];
  runsLoading: boolean;
  loadRuns: () => Promise<void>;
  selectedRunId: string | null;
  selectedRun: WorkflowRunDetail | null;
  selectRun: (runId: string | null) => Promise<void>;
  running: boolean;
  progress: RunProgress | null;
  runError: string | null;
  run: (documentIds: string[]) => Promise<void>;
  resume: (runId: string) => Promise<void>;
  cancel: () => Promise<void>;

  // ── Shared UI state ──
  editorOpen: boolean;
  setEditorOpen: (open: boolean) => void;
  pickerOpen: boolean;
  pickerDocs: RagDocument[];
  pickerLoading: boolean;
  openPicker: () => Promise<void>;
  closePicker: () => void;
}

const jobPoller = createJobPoller();

export const useWorkflowsStore = create<WorkflowsState>((set, get) => ({
  _initialized: false,
  workflows: [],
  workflowsLoading: false,
  templates: [],

  init: () => {
    if (get()._initialized) return;
    set({ _initialized: true });
    void get().loadWorkflows();
    void get().loadTemplates();
  },

  loadWorkflows: async () => {
    set({ workflowsLoading: true });
    try {
      const { workflows } = await api.listWorkflows();
      set({ workflows });
    } catch (e) {
      logger.error('loadWorkflows failed', e);
      toast(errorMessage(e, 'Could not load workflows.'), 'error');
    } finally {
      set({ workflowsLoading: false });
    }
  },

  loadTemplates: async () => {
    try {
      const { templates } = await api.listWorkflowTemplates();
      set({ templates });
    } catch (e) {
      logger.debug('loadWorkflowTemplates failed', e);
    }
  },

  createWorkflow: async ({ name, description, steps }) => {
    const trimmed = name.trim();
    if (!trimmed) {
      toast('Give the workflow a name.', 'error');
      return null;
    }
    if (steps.length === 0) {
      toast('Add at least one step.', 'error');
      return null;
    }
    try {
      const matterId = useMattersStore.getState().activeMatterId;
      const workflow = await api.createWorkflow({
        name: trimmed,
        description: description.trim() || null,
        matter_id: matterId,
        steps: cleanSteps(steps),
      });
      set((st) => ({ workflows: [workflowSummary(workflow), ...st.workflows] }));
      toast(`Created "${workflow.name}".`, 'success');
      return workflow;
    } catch (e) {
      logger.error('createWorkflow failed', e);
      toast(errorMessage(e, 'Could not create the workflow.'), 'error');
      return null;
    }
  },

  createFromTemplate: async (key) => {
    try {
      const matterId = useMattersStore.getState().activeMatterId;
      const workflow = await api.createWorkflowFromTemplate(key, matterId);
      set((st) => ({ workflows: [workflowSummary(workflow), ...st.workflows] }));
      toast(`Created "${workflow.name}" from the template. Edit its steps freely.`, 'success');
      return workflow;
    } catch (e) {
      logger.error('createWorkflowFromTemplate failed', e);
      toast(errorMessage(e, 'Could not create the workflow.'), 'error');
      return null;
    }
  },

  openWorkflow: (workflowId) => {
    appNavigate(`/workflows/${encodeURIComponent(workflowId)}`);
  },

  // ── One workflow ──
  workflowId: null,
  workflow: null,
  workflowLoading: false,
  workflowError: null,

  loadWorkflow: async (workflowId) => {
    if (get().workflowId !== workflowId) {
      jobPoller.stop();
      set({
        workflowId, workflow: null, workflowError: null, runs: [], selectedRunId: null, selectedRun: null,
        running: false, progress: null, runError: null, editorOpen: false, pickerOpen: false,
      });
    }
    set({ workflowLoading: true });
    try {
      const workflow = await api.getWorkflow(workflowId);
      if (get().workflowId !== workflowId) return;
      set({ workflow, workflowError: null, editorOpen: workflow.steps.length === 0 });
      void get().loadRuns();
    } catch (e) {
      logger.error('loadWorkflow failed', e);
      if (get().workflowId === workflowId) set({ workflowError: errorMessage(e, 'Could not load this workflow.') });
    } finally {
      if (get().workflowId === workflowId) set({ workflowLoading: false });
    }
  },

  saveDetails: async (name, description) => {
    const id = get().workflowId;
    if (!id || !name.trim()) return false;
    return applyUpdate(id, { name: name.trim(), description: description.trim() || null }, 'Saved.');
  },

  saveSteps: async (steps) => {
    const id = get().workflowId;
    if (!id) return false;
    if (steps.length === 0) {
      toast('Keep at least one step.', 'error');
      return false;
    }
    if (steps.some((s) => !s.name.trim())) {
      toast('Every step needs a name.', 'error');
      return false;
    }
    return applyUpdate(id, { steps: cleanSteps(steps) }, 'Steps saved. Past runs keep the steps they ran with.');
  },

  deleteWorkflow: () => {
    const workflow = get().workflow;
    if (!workflow) return;
    useUIStore.getState().showConfirm({
      title: `Delete "${workflow.name}"?`,
      message: 'The workflow and its run history are permanently deleted. Review tables, analyses and research runs its steps created are kept.',
      type: 'danger',
      confirmText: 'Delete workflow',
      onConfirm: async () => {
        try {
          await api.deleteWorkflow(workflow.id);
          jobPoller.stop();
          set((st) => ({
            workflows: st.workflows.filter((w) => w.id !== workflow.id),
            workflowId: null, workflow: null, runs: [], selectedRun: null, selectedRunId: null,
            running: false, progress: null,
          }));
          toast('Workflow deleted.', 'success');
          appNavigate('/workflows');
        } catch (e) {
          logger.error('deleteWorkflow failed', e);
          toast(errorMessage(e, 'Could not delete the workflow.'), 'error');
        }
      },
    });
  },

  // ── Runs ──
  runs: [],
  runsLoading: false,
  loadRuns: async () => {
    const id = get().workflowId;
    if (!id) return;
    set({ runsLoading: true });
    try {
      const { runs } = await api.listWorkflowRuns(id);
      if (get().workflowId !== id) return;
      set({ runs });
      // Keep the open run fresh, or open the newest one when nothing is selected.
      const selected = get().selectedRunId || runs[0]?.id || null;
      if (selected) await get().selectRun(selected);
    } catch (e) {
      logger.error('loadWorkflowRuns failed', e);
    } finally {
      if (get().workflowId === id) set({ runsLoading: false });
    }
  },

  selectedRunId: null,
  selectedRun: null,
  selectRun: async (runId) => {
    const id = get().workflowId;
    if (!id) return;
    if (!runId) {
      set({ selectedRunId: null, selectedRun: null });
      return;
    }
    set({ selectedRunId: runId });
    try {
      const run = await api.getWorkflowRun(id, runId);
      if (get().workflowId === id && get().selectedRunId === runId) set({ selectedRun: run });
    } catch (e) {
      logger.error('getWorkflowRun failed', e);
      toast(errorMessage(e, 'Could not load that run.'), 'error');
    }
  },

  running: false,
  progress: null,
  runError: null,

  run: async (documentIds) => {
    const id = get().workflowId;
    const workflow = get().workflow;
    if (!id || !workflow || get().running) return;
    if (workflow.steps.length === 0) {
      toast('Add at least one step before running.', 'error');
      return;
    }
    if (documentIds.length === 0 && !workflow.matter_id) {
      toast('Pick the documents to run the workflow on.', 'error');
      return;
    }
    try {
      const started = await api.runWorkflow(id, documentIds);
      set({ pickerOpen: false });
      await trackJob(id, started.run_id, started.job_id);
    } catch (e) {
      logger.error('runWorkflow failed', e);
      const message = errorMessage(e, 'Could not start the workflow.');
      set({ running: false, progress: null, runError: message });
      toast(message, 'error');
    }
  },

  resume: async (runId) => {
    const id = get().workflowId;
    if (!id || get().running) return;
    try {
      const started = await api.resumeWorkflowRun(id, runId);
      await trackJob(id, started.run_id, started.job_id);
    } catch (e) {
      logger.error('resumeWorkflowRun failed', e);
      const message = errorMessage(e, 'Could not resume the run.');
      set({ running: false, progress: null, runError: message });
      toast(message, 'error');
    }
  },

  cancel: async () => {
    const run = get().selectedRun;
    const jobId = run?.job_id;
    if (!jobId || !get().running) return;
    try {
      await api.cancelWorkflowJob(jobId);
      toast('Cancelling the run…', 'info');
    } catch (e) {
      logger.error('cancelWorkflowJob failed', e);
      toast(errorMessage(e, 'Could not cancel the run.'), 'error');
    }
  },

  // ── Shared UI state ──
  editorOpen: false,
  setEditorOpen: (open) => set({ editorOpen: open }),
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
      logger.error('workflow picker load failed', e);
      toast(errorMessage(e, 'Could not load your documents.'), 'error');
      set({ pickerDocs: [] });
    } finally {
      set({ pickerLoading: false });
    }
  },
  closePicker: () => set({ pickerOpen: false }),
}));

/** Poll the job behind a run, refreshing the run detail as steps complete. */
async function trackJob(workflowId: string, runId: string, jobId: string): Promise<void> {
  const st = useWorkflowsStore.getState();
  jobPoller.stop();
  useWorkflowsStore.setState({ running: true, runError: null, progress: { message: 'Starting…', fraction: 0 }, selectedRunId: runId });
  await st.loadRuns();
  let lastRefresh = 0;
  jobPoller.start({
    fetchJob: () => api.getWorkflowJob(jobId),
    maxAttempts: 1800,
    intervalMs: 2000,
    failedMessage: 'The workflow run failed.',
    timeoutMessage: 'Timed out waiting for the workflow to finish.',
    onPending: (job, attempt) => {
      const current = useWorkflowsStore.getState();
      if (current.workflowId !== workflowId) return;
      if (job.progress) useWorkflowsStore.setState({ progress: { message: job.progress.message, fraction: job.progress.fraction } });
      // Refresh the step list every few polls so completed steps appear as they finish.
      if (attempt - lastRefresh >= 3) {
        lastRefresh = attempt;
        void current.selectRun(runId);
      }
    },
    onCompleted: async (job) => {
      const current = useWorkflowsStore.getState();
      if (current.workflowId !== workflowId) return;
      useWorkflowsStore.setState({ running: false, progress: null });
      toast(describeRun(job.result), 'success');
      await current.loadRuns();
      void current.loadWorkflows();
    },
    onFailed: async (message) => {
      const current = useWorkflowsStore.getState();
      if (current.workflowId !== workflowId) return;
      useWorkflowsStore.setState({ running: false, progress: null, runError: message });
      toast(message, 'error');
      await current.loadRuns(); // whatever finished before the failure is already stored
      void current.loadWorkflows();
    },
  });
}

function workflowSummary(workflow: WorkflowDetail): WorkflowSummary {
  const { steps: _steps, ...summary } = workflow;
  return summary;
}

/** Trim names and drop empty optional strings so the server stores exactly what was meant. */
function cleanSteps(steps: WorkflowStep[]): WorkflowStep[] {
  return steps.map((s) => {
    const params: Record<string, unknown> = {};
    for (const [key, value] of Object.entries(s.params || {})) {
      if (typeof value === 'string') {
        const trimmed = value.trim();
        if (trimmed || key === 'instructions' || key === 'question') params[key] = trimmed;
      } else if (value !== undefined) {
        params[key] = value;
      }
    }
    return {
      name: s.name.trim(),
      kind: s.kind,
      params,
      inputs: {
        source: s.inputs.source,
        document_ids: s.inputs.source === 'documents' ? s.inputs.document_ids : [],
        step: s.inputs.source === 'step' ? s.inputs.step : null,
      },
    };
  });
}

/** PATCH the open workflow and replace it in the store. */
async function applyUpdate(
  id: string,
  changes: Parameters<typeof api.updateWorkflow>[1],
  successMessage: string,
): Promise<boolean> {
  try {
    const workflow = await api.updateWorkflow(id, changes);
    const st = useWorkflowsStore.getState();
    if (st.workflowId !== id) return true;
    useWorkflowsStore.setState((prev) => ({
      workflow,
      workflows: prev.workflows.map((w) => (w.id === id ? workflowSummary(workflow) : w)),
    }));
    toast(successMessage, 'success');
    return true;
  } catch (e) {
    logger.error('updateWorkflow failed', e);
    toast(errorMessage(e, 'Could not save the change.'), 'error');
    return false;
  }
}

registerReset(() => {
  jobPoller.stop();
  useWorkflowsStore.setState(useWorkflowsStore.getInitialState(), true);
});
