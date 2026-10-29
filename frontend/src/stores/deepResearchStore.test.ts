import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

vi.mock('../api', () => ({
  api: {
    planDeepResearch: vi.fn(),
    runDeepResearch: vi.fn(),
    listDeepResearch: vi.fn(),
    getDeepResearch: vi.fn(),
    deleteDeepResearch: vi.fn(),
    getDeepResearchJob: vi.fn(),
    cancelDeepResearchJob: vi.fn(),
    listMatters: vi.fn().mockResolvedValue({ matters: [] }),
  },
  API_BASE_URL: 'http://localhost:8000/api/v1',
}));

vi.mock('../utils/logger', () => ({ default: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() } }));

import { useDeepResearchStore, toDocumentFilter, describeResult, newStepId } from './deepResearchStore';
import { useMattersStore } from './mattersStore';
import { useUIStore } from './uiStore';
import { api } from '../api';

const plan = [
  { id: 'q1', question: 'What does the MSA say about liability?', tools: ['documents' as const] },
  { id: 'q2', question: 'Are liability caps enforceable?', tools: ['case_law' as const] },
];
const scope = { matter_id: null, document_ids: null, documents_in_scope: 3, case_law_available: true, citation_check_available: true };
const planResponse = { question: 'Is the cap enforceable?', plan, scope, caps: { max_subquestions: 8, max_tool_calls: 24, max_passages: 60 }, model: 'gpt' };

const result = {
  question: 'Is the cap enforceable?',
  answer: 'Yes [P1, P2].',
  key_findings: [],
  open_questions: [],
  sub_questions: [],
  sources: [{ ref: 'P1' }, { ref: 'P2' }],
  scope: { steps_failed: 0, tool_calls: 2 },
  verification: { quotes_verified: 2, quotes_unverified: 1, invalid_refs_stripped: 0, case_references_removed: [], citations_checked: 1 },
  model: 'gpt',
  notice: 'AI-generated.',
} as any;

const summary = {
  id: 'r1', owner_id: 'me', matter_id: null, role: 'owner', question: 'Is the cap enforceable?',
  status: 'completed', sub_questions: 2, sources_cited: 2, created_at: '2026-10-01T00:00:00Z', completed_at: '2026-10-01T00:01:00Z',
};
const detail = { ...summary, plan, scope: { matter_id: null, document_ids: null, documents_in_scope: 3 }, job_id: 'j1', result, error: null };

const flush = () => vi.advanceTimersByTimeAsync(0);

describe('deepResearchStore', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    useDeepResearchStore.setState(useDeepResearchStore.getInitialState(), true);
    useMattersStore.setState(useMattersStore.getInitialState(), true);
    useUIStore.setState(useUIStore.getInitialState(), true);
    vi.clearAllMocks();
    (api.planDeepResearch as any).mockResolvedValue(planResponse);
    (api.listDeepResearch as any).mockResolvedValue({ runs: [summary] });
    (api.getDeepResearch as any).mockResolvedValue(detail);
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  // ==================== helpers ====================

  it('toDocumentFilter sends only a real pick', () => {
    expect(toDocumentFilter(null)).toBeNull();
    expect(toDocumentFilter({ include_subfolders: true })).toBeNull();
    expect(toDocumentFilter({ document_ids: ['d1'], include_subfolders: true })).toEqual({
      document_ids: ['d1'], folder_paths: undefined, sources: undefined, doc_types: undefined, include_subfolders: true, search_all: undefined,
    });
  });

  it('describeResult summarises the counters honestly', () => {
    expect(describeResult(result)).toBe('Deep research finished: 2 sources cited, 2 quotes verified, 1 unverified.');
    expect(describeResult(null)).toBe('Deep research finished.');
  });

  it('newStepId never collides with an existing id', () => {
    const id = newStepId([{ id: 'u1', question: '', tools: [] }, { id: 'u2', question: '', tools: [] }]);
    expect(['u1', 'u2']).not.toContain(id);
  });

  // ==================== plan ====================

  it('draftPlan sends the question with the active matter and keeps the plan, scope and caps', async () => {
    useMattersStore.setState({ activeMatterId: 'm1' });
    useDeepResearchStore.getState().setQuestion('Is the cap enforceable?');
    await useDeepResearchStore.getState().draftPlan({ document_ids: ['d1'], include_subfolders: true });
    expect(api.planDeepResearch).toHaveBeenCalledWith({
      question: 'Is the cap enforceable?',
      matter_id: 'm1',
      document_filter: { document_ids: ['d1'], folder_paths: undefined, sources: undefined, doc_types: undefined, include_subfolders: true, search_all: undefined },
    });
    const st = useDeepResearchStore.getState();
    expect(st.plan).toEqual(plan);
    expect(st.planScope).toEqual(scope);
    expect(st.caps.max_subquestions).toBe(8);
    expect(st.planning).toBe(false);
  });

  it('draftPlan refuses an empty question and records a server failure', async () => {
    await useDeepResearchStore.getState().draftPlan(null);
    expect(api.planDeepResearch).not.toHaveBeenCalled();
    expect(useUIStore.getState().toasts.some((t) => t.message.includes('research question'))).toBe(true);

    (api.planDeepResearch as any).mockRejectedValue(new Error('No key'));
    useDeepResearchStore.getState().setQuestion('Something?');
    await useDeepResearchStore.getState().draftPlan(null);
    expect(useDeepResearchStore.getState().planError).toBe('No key');
  });

  it('steps can be edited, re-targeted, removed and added up to the cap', () => {
    useDeepResearchStore.setState({ plan, caps: { max_subquestions: 3 } });
    const st = useDeepResearchStore.getState();
    st.updateStep('q1', { question: 'Edited', tools: ['documents', 'case_law'] });
    expect(useDeepResearchStore.getState().plan[0]).toEqual({ id: 'q1', question: 'Edited', tools: ['documents', 'case_law'] });
    st.removeStep('q2');
    expect(useDeepResearchStore.getState().plan.map((s) => s.id)).toEqual(['q1']);
    st.addStep();
    st.addStep();
    expect(useDeepResearchStore.getState().plan).toHaveLength(3);
    st.addStep(); // over the cap: refused with a toast
    expect(useDeepResearchStore.getState().plan).toHaveLength(3);
    expect(useUIStore.getState().toasts.some((t) => t.message.includes('at most 3'))).toBe(true);
  });

  // ==================== run ====================

  it('run posts the cleaned plan, polls the job and keeps the result', async () => {
    useDeepResearchStore.setState({
      question: 'Is the cap enforceable?',
      plan: [...plan, { id: 'u1', question: '  ', tools: ['documents'] }], // the blank step is dropped
    });
    (api.runDeepResearch as any).mockResolvedValue({ run_id: 'r1', job_id: 'j1', poll_url: '/api/v1/jobs/j1', sub_questions: 2 });
    (api.getDeepResearchJob as any)
      .mockResolvedValueOnce({ job_id: 'j1', status: 'processing', progress: { message: 'Researched 1/2 sub-questions', fraction: 0.45 } })
      .mockResolvedValueOnce({ job_id: 'j1', status: 'completed', result });

    const running = useDeepResearchStore.getState().run(null);
    await flush();
    expect(api.runDeepResearch).toHaveBeenCalledWith({
      question: 'Is the cap enforceable?', matter_id: null, document_filter: null, plan,
    });
    expect(useDeepResearchStore.getState().running).toBe(true);
    expect(useDeepResearchStore.getState().runId).toBe('r1');

    await vi.advanceTimersByTimeAsync(10); // first poll: still processing
    expect(useDeepResearchStore.getState().progress).toEqual({ message: 'Researched 1/2 sub-questions', fraction: 0.45 });
    await vi.advanceTimersByTimeAsync(2000); // second poll: completed
    await running;
    await flush();

    const st = useDeepResearchStore.getState();
    expect(st.running).toBe(false);
    expect(st.result).toEqual(result);
    expect(st.resultRun?.id).toBe('r1');
    expect(api.listDeepResearch).toHaveBeenCalled();
    expect(useUIStore.getState().toasts.some((t) => t.message.startsWith('Deep research finished'))).toBe(true);
  });

  it('run reports a failed job and refuses an empty plan', async () => {
    await useDeepResearchStore.getState().run(null);
    expect(api.runDeepResearch).not.toHaveBeenCalled();

    useDeepResearchStore.setState({ question: 'Q?', plan });
    (api.runDeepResearch as any).mockResolvedValue({ run_id: 'r2', job_id: 'j2', poll_url: '', sub_questions: 2 });
    (api.getDeepResearchJob as any).mockResolvedValue({ job_id: 'j2', status: 'failed', error: 'synthesis down' });
    await useDeepResearchStore.getState().run(null);
    await vi.advanceTimersByTimeAsync(10);
    const st = useDeepResearchStore.getState();
    expect(st.running).toBe(false);
    expect(st.runError).toBe('synthesis down');
  });

  it('cancel asks the server to cancel the job and stops the run', async () => {
    useDeepResearchStore.setState({ running: true, jobId: 'j1' });
    (api.cancelDeepResearchJob as any).mockResolvedValue({ status: 'cancelled', job_id: 'j1' });
    await useDeepResearchStore.getState().cancel();
    expect(api.cancelDeepResearchJob).toHaveBeenCalledWith('j1');
    expect(useDeepResearchStore.getState().running).toBe(false);
    expect(useDeepResearchStore.getState().runError).toBe('Cancelled.');
  });

  // ==================== history ====================

  it('setOpen loads the history for the active matter and openRun restores a past run', async () => {
    useMattersStore.setState({ activeMatterId: 'm1' });
    useDeepResearchStore.getState().setOpen(true);
    await flush();
    expect(api.listDeepResearch).toHaveBeenCalledWith('m1');
    expect(useDeepResearchStore.getState().runs).toEqual([summary]);

    await useDeepResearchStore.getState().openRun('r1');
    const st = useDeepResearchStore.getState();
    expect(st.result).toEqual(result);
    expect(st.question).toBe('Is the cap enforceable?');
    expect(st.plan).toEqual(plan);
    expect(st.runError).toBeNull();

    (api.getDeepResearch as any).mockResolvedValue({ ...detail, status: 'failed', result: null, error: 'boom' });
    await useDeepResearchStore.getState().openRun('r1');
    expect(useDeepResearchStore.getState().runError).toBe('boom');
  });

  it('deleteRun confirms, then removes the run and clears it if open', async () => {
    useDeepResearchStore.setState({ runs: [summary], runId: 'r1', result, resultRun: detail as any });
    (api.deleteDeepResearch as any).mockResolvedValue({ status: 'deleted', id: 'r1' });
    useDeepResearchStore.getState().deleteRun('r1');
    expect(api.deleteDeepResearch).not.toHaveBeenCalled();
    expect(useUIStore.getState().confirmModal.open).toBe(true);
    useUIStore.getState().handleConfirm();
    await flush();
    expect(api.deleteDeepResearch).toHaveBeenCalledWith('r1');
    const st = useDeepResearchStore.getState();
    expect(st.runs).toEqual([]);
    expect(st.result).toBeNull();
    expect(st.runId).toBeNull();
  });
});
