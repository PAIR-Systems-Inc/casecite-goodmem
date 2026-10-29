import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('../api', () => ({
  api: {
    approveDeviceCode: vi.fn(),
    denyDeviceCode: vi.fn(),
  },
  API_BASE_URL: 'http://localhost:8000/api/v1',
}));

import { useDeviceApprovalStore } from './deviceApprovalStore';
import { api } from '../api';

describe('deviceApprovalStore', () => {
  beforeEach(() => {
    useDeviceApprovalStore.getState().reset();
    vi.clearAllMocks();
  });

  it('normalises typed codes and prefills only an empty field', () => {
    const store = useDeviceApprovalStore.getState();
    store.setUserCode('abcd-efgh ');
    expect(useDeviceApprovalStore.getState().userCode).toBe('ABCDEFGH');
    store.prefill('ZZZZ-ZZZZ');
    expect(useDeviceApprovalStore.getState().userCode).toBe('ABCDEFGH');
    store.reset();
    store.prefill('zzzz-zzzz');
    expect(useDeviceApprovalStore.getState().userCode).toBe('ZZZZZZZZ');
    store.prefill(null);
    expect(useDeviceApprovalStore.getState().userCode).toBe('ZZZZZZZZ');
  });

  it('refuses to submit an incomplete code without calling the API', async () => {
    useDeviceApprovalStore.getState().setUserCode('ABC');
    await useDeviceApprovalStore.getState().approve();
    expect(api.approveDeviceCode).not.toHaveBeenCalled();
    expect(useDeviceApprovalStore.getState().error).toMatch(/eight-character/);
    await useDeviceApprovalStore.getState().deny();
    expect(api.denyDeviceCode).not.toHaveBeenCalled();
  });

  it('approve records the decision the server returns', async () => {
    (api.approveDeviceCode as any).mockResolvedValue({ status: 'approved', user_code: 'ABCD-EFGH' });
    useDeviceApprovalStore.getState().setUserCode('abcdefgh');
    await useDeviceApprovalStore.getState().approve();
    expect(api.approveDeviceCode).toHaveBeenCalledWith('ABCDEFGH');
    const state = useDeviceApprovalStore.getState();
    expect(state.status).toBe('approved');
    expect(state.decidedCode).toBe('ABCD-EFGH');
    expect(state.error).toBe('');
  });

  it('deny records the decision the server returns', async () => {
    (api.denyDeviceCode as any).mockResolvedValue({ status: 'denied', user_code: 'ABCD-EFGH' });
    useDeviceApprovalStore.getState().setUserCode('ABCDEFGH');
    await useDeviceApprovalStore.getState().deny();
    expect(api.denyDeviceCode).toHaveBeenCalledWith('ABCDEFGH');
    expect(useDeviceApprovalStore.getState().status).toBe('denied');
  });

  it('a server error returns to idle with the message and keeps the code', async () => {
    (api.approveDeviceCode as any).mockRejectedValue(new Error('That code is not waiting for approval.'));
    useDeviceApprovalStore.getState().setUserCode('ABCDEFGH');
    await useDeviceApprovalStore.getState().approve();
    const state = useDeviceApprovalStore.getState();
    expect(state.status).toBe('idle');
    expect(state.error).toBe('That code is not waiting for approval.');
    expect(state.userCode).toBe('ABCDEFGH');
  });

  it('ignores a second submit while one is in flight', async () => {
    let resolve: (v: unknown) => void = () => {};
    (api.approveDeviceCode as any).mockReturnValue(new Promise((r) => { resolve = r; }));
    useDeviceApprovalStore.getState().setUserCode('ABCDEFGH');
    const first = useDeviceApprovalStore.getState().approve();
    await useDeviceApprovalStore.getState().approve();
    await useDeviceApprovalStore.getState().deny();
    expect(api.approveDeviceCode).toHaveBeenCalledTimes(1);
    expect(api.denyDeviceCode).not.toHaveBeenCalled();
    resolve({ status: 'approved', user_code: 'ABCD-EFGH' });
    await first;
    expect(useDeviceApprovalStore.getState().status).toBe('approved');
  });

  it('reset clears everything', async () => {
    (api.approveDeviceCode as any).mockResolvedValue({ status: 'approved', user_code: 'ABCD-EFGH' });
    useDeviceApprovalStore.getState().setUserCode('ABCDEFGH');
    await useDeviceApprovalStore.getState().approve();
    useDeviceApprovalStore.getState().reset();
    const state = useDeviceApprovalStore.getState();
    expect(state).toMatchObject({ userCode: '', status: 'idle', error: '', decidedCode: '' });
  });
});
