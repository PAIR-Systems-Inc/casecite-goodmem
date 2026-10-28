import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

vi.mock('../api', () => ({
  api: {
    listWorkflows: vi.fn(),
    createWorkflow: vi.fn(),
    getWorkflow: vi.fn(),
    updateWorkflow: vi.fn(),
    deleteWorkflow: vi.fn(),
    listWorkflowTemplates: vi.fn(),
    createWorkflowFromTemplate: vi.fn(),
    runWorkflow: vi.fn(),
    listWorkflowRuns: vi.fn(),
    getWorkflowRun: vi.fn(),
    resumeWorkflowRun: vi.fn(),
    getWorkflowJob: vi.fn(),
    cancelWorkflowJob: vi.fn(),
    getMatterDocuments: vi.fn(),
    getAllDocuments: vi.fn(),
    listMatters: vi.fn().mockResolvedValue({ matters: [] }),
  },
  API_BASE_URL: 'http://localhost:8000/api/v1',
}));

vi.mock('../utils/logger', () => ({ default: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() } }));

const navigate = vi.fn();
vi.mock('../utils/router', () => ({ appNavigate: (to: string) => navigate(to) }));

import { useWorkflowsStore, newStep, describeRun, stepKindInfo } from './workflowsStore';
import { useMattersStore } from './mattersStore';
import { useUIStore } from './uiStore';
import { api } from '../api';
import type { WorkflowStep, WorkflowSummary } from '../api/types';

const steps: WorkflowStep[] = [
  { name: 'extract', kind: 'extract_fields', params: { fields: [{ key: 'term', name: 'Term', description: '', type: 'duration' }] }, inputs: { source: 'run', document_ids: [], step: null } },
  { name: 'review', kind: 'analyze_contract', params: { contract_type: 'auto', posture: 'balanced' }, inputs: { source: 'run', document_ids: [], step: null } },
];
const summary: WorkflowSummary = {
  id: 'w1', name: 'Intake', description: null, matter_id: null, owner_id: 'me', role: 'owner', template_key: null,
  step_count: 2, step_kinds: ['extract_fields', 'analyze_contract'], run_count: 0, last_run_at: null, last_run_status: null,
  created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z',
};
const detail = { ...summary, steps };
const runSummary = {
  id: 'r1', workflow_id: 'w1', runner_id: 'me', matter_id: null, status: 'failed', steps: 2, completed_steps: 1,
  documents: 1, job_id: 'j1', error: "Step 2/2 'review' failed: boom", resumed_count: 0,
  created_at: '2026-10-02T00:00:00Z', completed_at: '2026-10-02T00:01:00Z',
};
const runDetail = {
  ...runSummary,
  step_definitions: steps,
  input_document_ids: ['d1'],
  step_runs: [
    { index: 0, name: 'extract', kind: 'extract_fields', status: 'completed', output: { summary: 'ok', data: {}, document_ids: ['d1'], produced: {} }, input_document_ids: ['d1'], produced_ids: {}, error: null, started_at: null, finished_at: null },
    { index: 1, name: 'review', kind: 'analyze_contract', status: 'failed', output: null, input_document_ids: [], produced_ids: {}, error: 'boom', started_at: null, finished_at: null },
  ],
};

/** Settle pending promises and zero-delay timers under the fake clock. */
const flush = () => vi.advanceTimersByTimeAsync(0);

describe('workflowsStore', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    useWorkflowsStore.setState(useWorkflowsStore.getInitialState(), true);
    useMattersStore.setState(useMattersStore.getInitialState(), true);
    useUIStore.setState(useUIStore.getInitialState(), true);
    vi.clearAllMocks();
    (api.listWorkflows as any).mockResolvedValue({ workflows: [summary] });
    (api.listWorkflowTemplates as any).mockResolvedValue({ templates: [{ key: 'nda_intake', name: 'NDA intake', description: 'd', steps }] });
    (api.getWorkflow as any).mockResolvedValue(detail);
    (api.listWorkflowRuns as any).mockResolvedValue({ runs: [runSummary] });
    (api.getWorkflowRun as any).mockResolvedValue(runDetail);
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  // ==================== helpers ====================

  it('newStep picks a unique name and the kind defaults', () => {
    const first = newStep('extract_fields', []);
    expect(first.name).toBe('extract');
    expect(first.inputs).toEqual({ source: 'run', document_ids: [], step: null });
    expect(newStep('extract_fields', [first]).name).toBe('extract2');
    expect(newStep('deep_research', []).params).toEqual({ question: '' });
  });

  it('describeRun and stepKindInfo summarise for the UI', () => {
    expect(describeRun(null)).toBe('Workflow finished.');
    expect(describeRun({ run_id: 'r', status: 'completed', steps: 2, completed_steps: 2, documents: 3, produced: { review_tables: 1, analyses: 0 } }))
      .toBe('Workflow finished: 2 steps, produced 1 review tables.');
    expect(stepKindInfo('citation_check').title).toBe('Check citations');
    expect(stepKindInfo('mystery').title).toBe('mystery');
  });

  // ==================== list / create ====================

  it('init loads the workflows and templates once', async () => {
    useWorkflowsStore.getState().init();
    useWorkflowsStore.getState().init();
    await flush();
    expect(api.listWorkflows).toHaveBeenCalledTimes(1);
    expect(api.listWorkflowTemplates).toHaveBeenCalledTimes(1);
    expect(useWorkflowsStore.getState().workflows).toEqual([summary]);
    expect(useWorkflowsStore.getState().templates).toHaveLength(1);
  });

  it('createWorkflow sends the active matter and cleaned steps, then prepends the summary', async () => {
    useMattersStore.setState({ activeMatterId: 'm1' });
    (api.createWorkflow as any).mockResolvedValue(detail);
    const created = await useWorkflowsStore.getState().createWorkflow({
      name: '  Intake ',
      description: '',
      steps: [
        { name: ' extract ', kind: 'extract_fields', params: { fields: steps[0].params.fields, table_name: '  ' }, inputs: { source: 'documents', document_ids: ['d1'], step: 'x' } },
        { name: 'memo', kind: 'draft_document', params: { instructions: ' Write it ', include_outputs_from: ['extract'] }, inputs: { source: 'step', document_ids: ['zzz'], step: 'extract' } },
      ],
    });
    expect(created).toEqual(detail);
    expect(api.createWorkflow).toHaveBeenCalledWith({
      name: 'Intake',
      description: null,
      matter_id: 'm1',
      steps: [
        { name: 'extract', kind: 'extract_fields', params: { fields: steps[0].params.fields }, inputs: { source: 'documents', document_ids: ['d1'], step: null } },
        { name: 'memo', kind: 'draft_document', params: { instructions: 'Write it', include_outputs_from: ['extract'] }, inputs: { source: 'step', document_ids: [], step: 'extract' } },
      ],
    });
    expect(useWorkflowsStore.getState().workflows[0].id).toBe('w1');
  });

  it('createWorkflow refuses an empty name or step list without calling the API', async () => {
    expect(await useWorkflowsStore.getState().createWorkflow({ name: '', description: '', steps })).toBeNull();
    expect(await useWorkflowsStore.getState().createWorkflow({ name: 'x', description: '', steps: [] })).toBeNull();
    expect(api.createWorkflow).not.toHaveBeenCalled();
    expect(useUIStore.getState().toasts.length).toBe(2);
  });

  it('createFromTemplate passes the active matter and returns the new workflow', async () => {
    useMattersStore.setState({ activeMatterId: 'm1' });
    (api.createWorkflowFromTemplate as any).mockResolvedValue({ ...detail, template_key: 'nda_intake' });
    const created = await useWorkflowsStore.getState().createFromTemplate('nda_intake');
    expect(api.createWorkflowFromTemplate).toHaveBeenCalledWith('nda_intake', 'm1');
    expect(created?.template_key).toBe('nda_intake');
    expect(useWorkflowsStore.getState().workflows[0].id).toBe('w1');
  });

  it('openWorkflow navigates to the workflow route', () => {
    useWorkflowsStore.getState().openWorkflow('w 1');
    expect(navigate).toHaveBeenCalledWith('/workflows/w%201');
  });

  // ==================== one workflow ====================

  it('loadWorkflow loads the workflow, its runs and the newest run detail', async () => {
    await useWorkflowsStore.getState().loadWorkflow('w1');
    await flush();
    const st = useWorkflowsStore.getState();
    expect(st.workflow).toEqual(detail);
    expect(st.runs).toEqual([runSummary]);
    expect(st.selectedRunId).toBe('r1');
    expect(st.selectedRun?.step_runs[1].status).toBe('failed');
    expect(st.editorOpen).toBe(false);
  });

  it('loadWorkflow records an error for an unreadable workflow', async () => {
    (api.getWorkflow as any).mockRejectedValue(new Error('Workflow not found'));
    await useWorkflowsStore.getState().loadWorkflow('nope');
    expect(useWorkflowsStore.getState().workflowError).toBe('Workflow not found');
    expect(useWorkflowsStore.getState().workflow).toBeNull();
  });

  it('saveSteps PATCHes the cleaned steps and replaces the workflow', async () => {
    useWorkflowsStore.setState({ workflows: [summary] });
    await useWorkflowsStore.getState().loadWorkflow('w1');
    await flush();
    (api.updateWorkflow as any).mockResolvedValue({ ...detail, step_count: 1, steps: [steps[1]] });
    const ok = await useWorkflowsStore.getState().saveSteps([{ ...steps[1], name: ' review ' }]);
    expect(ok).toBe(true);
    expect(api.updateWorkflow).toHaveBeenCalledWith('w1', { steps: [{ ...steps[1], name: 'review' }] });
    expect(useWorkflowsStore.getState().workflow?.step_count).toBe(1);
    expect(useWorkflowsStore.getState().workflows[0].step_count).toBe(1);
  });

  it('saveSteps refuses an empty list or a nameless step without calling the API', async () => {
    await useWorkflowsStore.getState().loadWorkflow('w1');
    expect(await useWorkflowsStore.getState().saveSteps([])).toBe(false);
    expect(await useWorkflowsStore.getState().saveSteps([{ ...steps[0], name: '  ' }])).toBe(false);
    expect(api.updateWorkflow).not.toHaveBeenCalled();
  });

  it('deleteWorkflow asks for confirmation, then deletes and navigates back', async () => {
    await useWorkflowsStore.getState().loadWorkflow('w1');
    (api.deleteWorkflow as any).mockResolvedValue({ status: 'deleted', id: 'w1' });
    useWorkflowsStore.getState().deleteWorkflow();
    const confirm = useUIStore.getState().confirmModal;
    expect(confirm.open).toBe(true);
    expect(api.deleteWorkflow).not.toHaveBeenCalled();
    await useUIStore.getState().handleConfirm();
    await flush();
    expect(api.deleteWorkflow).toHaveBeenCalledWith('w1');
    expect(useWorkflowsStore.getState().workflow).toBeNull();
    expect(useWorkflowsStore.getState().workflows).toEqual([]);
    expect(navigate).toHaveBeenCalledWith('/workflows');
  });

  // ==================== runs ====================

  it('run starts the job, polls it, and refreshes the runs on completion', async () => {
    await useWorkflowsStore.getState().loadWorkflow('w1');
    await flush();
    (api.runWorkflow as any).mockResolvedValue({ run_id: 'r2', job_id: 'j2', poll_url: '/jobs/j2', steps: 2, documents: 1 });
    (api.getWorkflowJob as any)
      .mockResolvedValueOnce({ job_id: 'j2', status: 'processing', progress: { message: 'Step 1/2 (extract): starting', fraction: 0 } })
      .mockResolvedValueOnce({ job_id: 'j2', status: 'completed', result: { run_id: 'r2', status: 'completed', steps: 2, completed_steps: 2, documents: 1, produced: { analyses: 1 } } });

    const runPromise = useWorkflowsStore.getState().run(['d1']);
    await flush();
    await runPromise;
    expect(api.runWorkflow).toHaveBeenCalledWith('w1', ['d1']);
    expect(useWorkflowsStore.getState().running).toBe(true);
    expect(useWorkflowsStore.getState().selectedRunId).toBe('r2');

    await flush(); // first poll: processing
    expect(useWorkflowsStore.getState().progress?.message).toBe('Step 1/2 (extract): starting');
    await vi.advanceTimersByTimeAsync(2000); // second poll: completed
    await flush();
    expect(useWorkflowsStore.getState().running).toBe(false);
    expect(useUIStore.getState().toasts.some((t) => t.message.includes('produced 1 analyses'))).toBe(true);
    expect(api.listWorkflowRuns).toHaveBeenCalledTimes(3); // load, start, completion
  });

  it('run refuses to start without documents for a private workflow', async () => {
    await useWorkflowsStore.getState().loadWorkflow('w1');
    await useWorkflowsStore.getState().run([]);
    expect(api.runWorkflow).not.toHaveBeenCalled();
    expect(useUIStore.getState().toasts[0].message).toBe('Pick the documents to run the workflow on.');
  });

  it('run records a failed job and keeps whatever finished', async () => {
    await useWorkflowsStore.getState().loadWorkflow('w1');
    await flush();
    (api.runWorkflow as any).mockResolvedValue({ run_id: 'r2', job_id: 'j2', poll_url: '/jobs/j2', steps: 2, documents: 1 });
    (api.getWorkflowJob as any).mockResolvedValue({ job_id: 'j2', status: 'failed', error: "Step 'review' failed: boom" });
    await useWorkflowsStore.getState().run(['d1']);
    await flush();
    const st = useWorkflowsStore.getState();
    expect(st.running).toBe(false);
    expect(st.runError).toBe("Step 'review' failed: boom");
  });

  it('resume re-submits the run and cancel deletes the job', async () => {
    await useWorkflowsStore.getState().loadWorkflow('w1');
    await flush();
    (api.resumeWorkflowRun as any).mockResolvedValue({ run_id: 'r1', job_id: 'j3', poll_url: '/jobs/j3', steps: 2, documents: 1 });
    (api.getWorkflowJob as any).mockResolvedValue({ job_id: 'j3', status: 'processing', progress: null });
    (api.getWorkflowRun as any).mockResolvedValue({ ...runDetail, status: 'running', job_id: 'j3' });
    await useWorkflowsStore.getState().resume('r1');
    await flush();
    expect(api.resumeWorkflowRun).toHaveBeenCalledWith('w1', 'r1');
    expect(useWorkflowsStore.getState().running).toBe(true);

    (api.cancelWorkflowJob as any).mockResolvedValue({ status: 'cancelled', job_id: 'j3' });
    await useWorkflowsStore.getState().cancel();
    expect(api.cancelWorkflowJob).toHaveBeenCalledWith('j3');
  });

  // ==================== picker ====================

  it('openPicker lists the active matter documents, else everything readable', async () => {
    (api.getAllDocuments as any).mockResolvedValue([{ id: 'd1', filename: 'a.pdf', status: 'indexed' }]);
    await useWorkflowsStore.getState().openPicker();
    expect(api.getAllDocuments).toHaveBeenCalled();
    expect(useWorkflowsStore.getState().pickerDocs).toHaveLength(1);

    useMattersStore.setState({ activeMatterId: 'm1' });
    (api.getMatterDocuments as any).mockResolvedValue({ documents: [{ id: 'd2', filename: 'b.pdf', status: 'indexed' }] });
    await useWorkflowsStore.getState().openPicker();
    expect(api.getMatterDocuments).toHaveBeenCalledWith('m1');
    expect(useWorkflowsStore.getState().pickerDocs[0].id).toBe('d2');
    useWorkflowsStore.getState().closePicker();
    expect(useWorkflowsStore.getState().pickerOpen).toBe(false);
  });
});
