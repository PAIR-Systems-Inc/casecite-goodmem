import { beforeEach, describe, expect, it, vi } from 'vitest';
import { api, DemoSessionExpired } from '../api';
import { usePublicDemoStore } from './publicDemoStore';

vi.mock('../api', () => ({
  api: { demoChat: vi.fn(), demoDocuments: vi.fn(), demoSession: vi.fn(), demoLogout: vi.fn() },
  DemoSessionExpired: class extends Error {},
}));

const session = { private: true as const, uploads_used: 1, upload_limit: 10, expires_at: '2027-01-01T12:00:00Z' };

describe('private demo view boundaries', () => {
  beforeEach(() => {
    vi.resetAllMocks();
    usePublicDemoStore.setState({ mode: 'demo', scope: 'private', session, documents: [], messages: [], question: 'Private question', error: '', busy: false, epoch: 0 });
    vi.mocked(api.demoDocuments).mockResolvedValue([]);
    vi.mocked(api.demoLogout).mockResolvedValue();
  });

  it('discards a private answer that arrives after switching to public documents', async () => {
    let resolve!: (answer: { answer: string; sources: [] }) => void;
    vi.mocked(api.demoChat).mockImplementation(() => new Promise((done) => { resolve = done; }));
    const question = usePublicDemoStore.getState().ask();
    await usePublicDemoStore.getState().select('public');
    resolve({ answer: 'Private evidence', sources: [] });
    await question;
    expect(usePublicDemoStore.getState().messages).toEqual([]);
    expect(usePublicDemoStore.getState().scope).toBe('public');
  });

  it('clears private documents and answers when the server rejects an expired session', async () => {
    usePublicDemoStore.setState({ documents: [{ id: 'secret', filename: 'secret.txt', status: 'indexed' }], messages: [{ question: 'Private', answer: 'Secret', sources: [] }] });
    vi.mocked(api.demoDocuments).mockRejectedValue(new DemoSessionExpired('Invitation expired'));
    await usePublicDemoStore.getState().refresh();
    const state = usePublicDemoStore.getState();
    expect(state.documents).toEqual([]);
    expect(state.messages).toEqual([]);
    expect(state.session).toBeNull();
    expect(state.scope).toBe('public');
    expect(state.error).toBe('Invitation expired');
  });

  it('clears private content even when logout fails, and reports the failure', async () => {
    vi.mocked(api.demoLogout).mockRejectedValue(new Error('Network unavailable; reload to retry logout'));
    await usePublicDemoStore.getState().logout();
    expect(usePublicDemoStore.getState().messages).toEqual([]);
    expect(usePublicDemoStore.getState().session).toBeNull();
    expect(usePublicDemoStore.getState().error).toContain('Network unavailable');
  });
});
