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
