import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
vi.mock('../api', () => ({ api: { getWorkspaceSession: vi.fn(), updateWorkspaceSession: vi.fn(), createWorkspaceSession: vi.fn() } }));
vi.mock('../utils/router', () => ({ appNavigate: vi.fn() }));
const { restore } = vi.hoisted(() => ({ restore: vi.fn() }));
vi.mock('./judgeIntelStore', () => ({ useJudgeIntelStore: { getState: () => ({ restoreWorkspaceSession: restore }) } }));
import { api } from '../api';
import { appNavigate } from '../utils/router';
import { useWorkspaceSessionsStore as store } from './workspaceSessionsStore';

beforeEach(() => {
  vi.useFakeTimers();
  vi.clearAllMocks();
  store.getState().resetKey('judge');
  store.setState(store.getInitialState(), true);
});
afterEach(() => { store.getState().resetKey('judge'); vi.useRealTimers(); });

describe('judge history continuation', () => {
  it('continues saving to the restored session after rebuilding its profile', async () => {
    const row = { id: 'saved-judge', surface: 'judge', title: 'Judge: Jane Example', payload: { judgeId: '42' } };
    vi.mocked(api.getWorkspaceSession).mockResolvedValue(row);
    vi.mocked(api.updateWorkspaceSession).mockResolvedValue(row);
    restore.mockImplementation(async () => { store.getState().resetKey('judge'); return true; });
    await store.getState().restoreSession(row.id);
    store.getState().saveSession('judge', 'judge', row.title, { judgeId: '42', judgeBrief: 'Continued brief' });
    await vi.advanceTimersByTimeAsync(1500);
    expect(api.updateWorkspaceSession).toHaveBeenCalledWith(row.id, expect.objectContaining({ payload: { judgeId: '42', judgeBrief: 'Continued brief' } }));
    expect(api.createWorkspaceSession).not.toHaveBeenCalled();
    expect(appNavigate).toHaveBeenCalledWith('/judge-intel');
  });
  it('does not navigate back to a stale or failed profile restoration', async () => {
    vi.mocked(api.getWorkspaceSession).mockResolvedValue({ id: 'saved-judge', surface: 'judge', title: 'Judge: Jane Example', payload: { judgeId: '42' } });
    restore.mockResolvedValue(false);
    await store.getState().restoreSession('saved-judge');
    expect(appNavigate).not.toHaveBeenCalled();
    expect(store.getState().restoringId).toBeNull();
  });
});
