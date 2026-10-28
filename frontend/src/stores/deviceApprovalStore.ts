import { create } from 'zustand';
import { registerReset } from './resetRegistry';
import { api } from '../api';
import { isCompleteDeviceUserCode, normalizeDeviceUserCode } from '../utils/deviceCode';

/**
 * The /device page: a signed-in user approves or denies the code the Word
 * add-in is showing. Approving signs that add-in in as this account, so the
 * page says exactly that before the button.
 */
export type DeviceApprovalStatus = 'idle' | 'submitting' | 'approved' | 'denied';

export interface DeviceApprovalState {
  userCode: string;
  status: DeviceApprovalStatus;
  error: string;
  /** The code the last decision was recorded for, as the server displays it. */
  decidedCode: string;

  setUserCode: (raw: string) => void;
  /** Prefill from `?user_code=` once; a code typed afterwards is kept. */
  prefill: (raw: string | null) => void;
  approve: () => Promise<void>;
  deny: () => Promise<void>;
  reset: () => void;
}

const initialState = {
  userCode: '',
  status: 'idle' as DeviceApprovalStatus,
  error: '',
  decidedCode: '',
};

function errorMessage(e: unknown, fallback: string): string {
  return e instanceof Error && e.message ? e.message : fallback;
}

export const useDeviceApprovalStore = create<DeviceApprovalState>((set, get) => ({
  ...initialState,

  setUserCode: (raw) => set({ userCode: normalizeDeviceUserCode(raw), error: '' }),

  prefill: (raw) => {
    if (raw && !get().userCode) set({ userCode: normalizeDeviceUserCode(raw) });
  },

  approve: async () => {
    const { userCode, status } = get();
    if (status === 'submitting') return;
    if (!isCompleteDeviceUserCode(userCode)) {
      set({ error: 'Enter the eight-character code shown in Word.' });
      return;
    }
    set({ status: 'submitting', error: '' });
    try {
      const decision = await api.approveDeviceCode(userCode);
      set({ status: 'approved', decidedCode: decision.user_code });
    } catch (e: unknown) {
      set({ status: 'idle', error: errorMessage(e, 'Could not approve this code.') });
    }
  },

  deny: async () => {
    const { userCode, status } = get();
    if (status === 'submitting') return;
    if (!isCompleteDeviceUserCode(userCode)) {
      set({ error: 'Enter the eight-character code shown in Word.' });
      return;
    }
    set({ status: 'submitting', error: '' });
    try {
      const decision = await api.denyDeviceCode(userCode);
      set({ status: 'denied', decidedCode: decision.user_code });
    } catch (e: unknown) {
      set({ status: 'idle', error: errorMessage(e, 'Could not deny this code.') });
    }
  },

  reset: () => set({ ...initialState }),
}));

registerReset(() => useDeviceApprovalStore.getState().reset());
