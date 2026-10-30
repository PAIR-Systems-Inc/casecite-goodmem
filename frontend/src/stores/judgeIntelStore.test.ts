import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
vi.mock('../api', () => ({ api: { authFetch: vi.fn(), request: vi.fn() }, API_BASE_URL: 'http://localhost/api/v1' }));
vi.mock('./workspaceSessionsStore', () => ({ persistWorkspaceSession: vi.fn(), resetWorkspaceSessionKey: vi.fn() }));
import { api } from '../api';
import { useJudgeIntelStore as store } from './judgeIntelStore';

const response = (data: unknown, status = 200) => new Response(JSON.stringify(data), { status });
const profile = { id: '42', name: 'Jane Example' };
beforeEach(() => {
  store.getState().backToJudgeSearch();
  store.setState(store.getInitialState(), true);
  vi.clearAllMocks();
  vi.useFakeTimers();
});
afterEach(() => { store.getState().backToJudgeSearch(); vi.useRealTimers(); });

describe('judge build jobs', () => {
  it('shows progress and opens the completed profile', async () => {
    vi.mocked(api.authFetch).mockImplementation(async (url) => response(String(url).includes('/build/') ? { job_id: 'job1' } : {}, String(url).includes('/build/') ? 202 : 200));
    vi.mocked(api.request).mockResolvedValueOnce({ status: 'running', progress: { message: 'Reading separate opinions…' } }).mockResolvedValueOnce({ status: 'completed', result: { profile } });
    const build = store.getState().buildJudgeIntel('42', 'Jane Example');
    await vi.advanceTimersByTimeAsync(0);
    expect(store.getState().judgeIntelBuildProgress).toBe('Reading separate opinions…');
    expect(store.getState().judgeIntelBuilding).toBe(true);
    await vi.advanceTimersByTimeAsync(3000);
    await build;
    expect(store.getState().judgeIntelProfile).toEqual(profile);
    expect(store.getState().judgeIntelBuilding).toBe(false);
    expect(api.authFetch).toHaveBeenCalledWith(expect.stringContaining('/build/42?async=true'), { method: 'POST' });
  });
  it('opens a cached profile without polling', async () => {
    vi.mocked(api.authFetch).mockImplementation(async (url) => response(String(url).includes('/build/') ? { status: 'cached', profile } : {}));
    await store.getState().buildJudgeIntel('42', 'Jane Example');
    expect(store.getState().judgeIntelProfile).toEqual(profile);
    expect(api.request).not.toHaveBeenCalled();
  });
  it('discards late results after returning to search', async () => {
    let finish!: (value: unknown) => void;
    vi.mocked(api.authFetch).mockResolvedValue(response({ job_id: 'job1' }, 202));
    vi.mocked(api.request).mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
    const build = store.getState().buildJudgeIntel('42', 'Jane Example');
    await vi.advanceTimersByTimeAsync(0);
    store.getState().backToJudgeSearch();
    finish({ status: 'completed', result: { profile } });
    await build;
    await vi.advanceTimersByTimeAsync(0);
    expect(store.getState().judgeIntelProfile).toBeNull();
    expect(store.getState().judgeIntelBuilding).toBe(false);
  });
  it('surfaces failed jobs instead of opening an empty profile', async () => {
    vi.mocked(api.authFetch).mockResolvedValue(response({ job_id: 'job1' }, 202));
    vi.mocked(api.request).mockResolvedValue({ status: 'failed', error: 'CourtListener rejected the configured API token.' });
    await store.getState().buildJudgeIntel('42', 'Jane Example');
    expect(store.getState().judgeIntelProfile?.error).toContain('rejected');
    expect(store.getState().judgeIntelBuilding).toBe(false);
  });
});

const deferred = () => {
  let resolve!: (response: Response) => void;
  const promise = new Promise<Response>((done) => { resolve = done; });
  return { promise, resolve };
};

describe('judge brief and questions', () => {
  it('requests the server-held profile and preserves the document selector', async () => {
    const { persistWorkspaceSession } = await import('./workspaceSessionsStore');
    store.setState({ judgeIntelProfile: { ...profile, wikipedia_summary: 'CLIENT_FORGED_PROFILE' } });
    vi.mocked(api.authFetch).mockResolvedValue(response({ content: '## QUICK FACTS\nJane Example studied at Example University.' }));
    await store.getState().generateJudgeBrief(store.getState().judgeIntelProfile!);
    let body = JSON.parse(String(vi.mocked(api.authFetch).mock.calls[0][1]?.body));
    expect(body).toMatchObject({ judge_id: '42', judge_task: 'brief', include_documents: false, include_case_law: false });
    expect(body.query).not.toContain('CLIENT_FORGED_PROFILE');
    expect(store.getState().judgeBrief).toContain('QUICK FACTS');
    expect(store.getState().judgeBriefLoading).toBe(false);
    store.setState({ judgeQueryInput: 'What do my documents add?', judgeQueryIncludeDocs: true, judgeDocFilter: { document_ids: ['doc-1'] } });
    vi.mocked(api.authFetch).mockResolvedValue(response({ content: 'Notice v. Example is in the profile. The agreement requires notice.' }));
    await store.getState().queryJudgeContext();
    body = JSON.parse(String(vi.mocked(api.authFetch).mock.calls[1][1]?.body));
    expect(body).toMatchObject({ query: 'What do my documents add?', judge_id: '42', judge_task: 'question', include_documents: true, document_filter: { document_ids: ['doc-1'] } });
    expect(store.getState().judgeMessages.map((message) => message.type)).toEqual(['user', 'assistant']);
    expect(persistWorkspaceSession).toHaveBeenLastCalledWith('judge', 'judge', 'Judge: Jane Example', expect.objectContaining({ judgeId: '42', judgeMessages: store.getState().judgeMessages }));
  });

  it('aborts both requests on navigation and rejects responses even if transport ignores abort', async () => {
    const { persistWorkspaceSession } = await import('./workspaceSessionsStore');
    const brief = deferred();
    const answer = deferred();
    store.setState({ judgeIntelProfile: profile, judgeQueryInput: 'What is in this profile?' });
    vi.mocked(api.authFetch).mockReturnValueOnce(brief.promise).mockReturnValueOnce(answer.promise);
    const a = store.getState().generateJudgeBrief(profile);
    const b = store.getState().queryJudgeContext();
    const signals = vi.mocked(api.authFetch).mock.calls.map((call) => call[1]?.signal);
    store.getState().backToJudgeSearch();
    store.setState({ judgeIntelProfile: { id: '43', name: 'Another Judge' } });
    expect(signals.every((signal) => signal?.aborted)).toBe(true);
    brief.resolve(response({ content: 'Obsolete brief' }));
    answer.resolve(response({ content: 'Obsolete answer' }));
    await Promise.all([a, b]);
    expect(store.getState()).toMatchObject({ judgeBrief: null, judgeMessages: [], judgeBriefLoading: false, judgeQueryLoading: false });
    expect(persistWorkspaceSession).not.toHaveBeenCalled();
  });

  it('clear cancels the old answer without cancelling a new question', async () => {
    const old = deferred();
    const fresh = deferred();
    store.setState({ judgeIntelProfile: profile, judgeQueryInput: 'Old question' });
    vi.mocked(api.authFetch).mockReturnValueOnce(old.promise).mockReturnValueOnce(fresh.promise);
    const a = store.getState().queryJudgeContext();
    store.getState().clearJudgeConversation();
    store.setState({ judgeQueryInput: 'New question' });
    const b = store.getState().queryJudgeContext();
    old.resolve(response({ content: 'Old answer' }));
    await a;
    expect(store.getState().judgeQueryLoading).toBe(true);
    fresh.resolve(response({ content: 'New answer' }));
    await b;
    expect(store.getState().judgeMessages.map((message) => message.content)).toEqual(['New question', 'New answer']);
  });

  it('keeps the last successful brief if regeneration fails and surfaces a question error', async () => {
    store.setState({ judgeIntelProfile: profile, judgeBrief: 'Last successful brief' });
    vi.mocked(api.authFetch).mockImplementation(async () => response({ detail: 'The judge profile changed. Submit again.' }, 409));
    await store.getState().generateJudgeBrief(profile);
    expect(store.getState().judgeBrief).toBe('Last successful brief');
    expect(store.getState().judgeBriefLoading).toBe(false);
    store.setState({ judgeQueryInput: 'Question' });
    await store.getState().queryJudgeContext();
    expect(store.getState().judgeMessages[store.getState().judgeMessages.length - 1]?.content).toContain('profile changed');
    expect(store.getState().judgeQueryLoading).toBe(false);
  });

  it('does not duplicate model requests while an answer is running', async () => {
    const pending = deferred();
    store.setState({ judgeIntelProfile: profile, judgeQueryInput: 'First question' });
    vi.mocked(api.authFetch).mockReturnValue(pending.promise);
    const a = store.getState().queryJudgeContext();
    store.setState({ judgeQueryInput: 'Second question' });
    await store.getState().queryJudgeContext();
    expect(api.authFetch).toHaveBeenCalledTimes(1);
    pending.resolve(response({ content: 'First answer' }));
    await a;
    expect(store.getState().judgeQueryInput).toBe('Second question');
  });
});

describe('judge navigation races', () => {
  it('does not restore old history over a newly opened profile', async () => {
    const old = deferred();
    vi.mocked(api.authFetch).mockImplementation((url) => String(url).includes('/build/42') ? old.promise : Promise.resolve(response(String(url).includes('/build/43') ? { profile: { id: '43', name: 'Another Judge' } } : {})));
    const restore = store.getState().restoreWorkspaceSession({ judgeId: '42', judgeMessages: [{ id: 1, type: 'assistant', content: 'Old history' }], judgeBrief: 'Old brief' });
    await store.getState().buildJudgeIntel('43', 'Another Judge');
    old.resolve(response({ profile }));
    await restore;
    expect(store.getState()).toMatchObject({ judgeIntelProfile: { id: '43' }, judgeMessages: [], judgeBrief: null });
  });

  it('discards stale statistics, metrics and opinion text after navigation', async () => {
    const pending = deferred();
    store.setState({ judgeIntelProfile: profile });
    vi.mocked(api.authFetch).mockReturnValue(pending.promise);
    const requests = [store.getState().loadJudgeIntelStats('42'), store.getState().loadJudgeIntelMetrics('42'), store.getState().loadOpinionFullText('42', '77')];
    store.getState().backToJudgeSearch();
    pending.resolve(response({ total: 999, full_text: 'Old opinion' }));
    await Promise.all(requests);
    expect(store.getState()).toMatchObject({ judgeIntelStats: null, judgeIntelMetrics: null, selectedOpinion: null, judgeIntelMetricsLoading: false });
  });

  it('keeps the latest opinion query when an earlier search finishes late', async () => {
    const old = deferred();
    store.setState({ judgeIntelProfile: profile });
    vi.mocked(api.authFetch).mockReturnValueOnce(old.promise).mockResolvedValueOnce(response({ opinions: [{ id: 2, case_name: 'New result' }], total: 1 }));
    const a = store.getState().queryJudgeOpinions('42', 'old');
    await store.getState().queryJudgeOpinions('42', 'new');
    old.resolve(response({ opinions: [{ id: 1, case_name: 'Old result' }], total: 1 }));
    await a;
    expect(store.getState().judgeIntelOpinions?.opinions?.[0].case_name).toBe('New result');
  });
});
