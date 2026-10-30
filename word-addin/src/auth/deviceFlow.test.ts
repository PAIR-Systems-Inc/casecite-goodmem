import { describe, it, expect, vi } from 'vitest';
import type { ApiClient } from '../api/client';
import type { DeviceStart, DeviceTokenResponse } from '../api/types';
import { DeviceFlowError, nextInterval, runDeviceFlow } from './deviceFlow';

const START: DeviceStart = {
  device_code: 'secret-device-code',
  user_code: 'ABCD-EFGH',
  verification_uri: 'https://casecite.example.com/device',
  verification_uri_complete: 'https://casecite.example.com/device?user_code=ABCD-EFGH',
  expires_in: 600,
  interval: 5,
};

function fakeClient(polls: DeviceTokenResponse[]) {
  const setTokens = vi.fn();
  const pollDeviceToken = vi.fn(async () => {
    const next = polls.shift();
    if (!next) throw new Error('polled too often');
    return next;
  });
  const client = {
    startDeviceFlow: vi.fn(async () => START),
    pollDeviceToken,
    setTokens,
  } as unknown as ApiClient;
  return { client, setTokens, pollDeviceToken };
}

describe('nextInterval', () => {
  it('stretches by five seconds on slow_down and otherwise keeps the interval', () => {
    expect(nextInterval(5, 'slow_down')).toBe(10);
    expect(nextInterval(10, 'authorization_pending')).toBe(10);
  });
});

describe('runDeviceFlow', () => {
  it('reports the code, waits the interval between polls, and returns the approved tokens', async () => {
    const { client, setTokens } = fakeClient([
      { status: 'authorization_pending' },
      { status: 'slow_down' },
      { status: 'authorization_pending' },
      {
        status: 'approved',
        access_token: 'a1',
        refresh_token: 'r1',
        expires_in: 3600,
        user: { id: 'u1', email: 'x@y.z', name: 'X', roles: ['attorney'] },
      },
    ]);
    const onCode = vi.fn();
    const waits: number[] = [];
    const sleep = vi.fn(async (ms: number) => {
      waits.push(ms);
    });

    const result = await runDeviceFlow(client, { onCode, sleep });

    expect(onCode).toHaveBeenCalledWith(START);
    expect(result.tokens).toEqual({ accessToken: 'a1', refreshToken: 'r1' });
    expect(result.user.email).toBe('x@y.z');
    expect(setTokens).toHaveBeenCalledWith({ accessToken: 'a1', refreshToken: 'r1' });
    // 5 s, 5 s, then 10 s after the slow_down, then the approving poll's 10 s.
    expect(waits).toEqual([5000, 5000, 10000, 10000]);
  });

  it('fails with "denied" when the web user refuses the code', async () => {
    const { client } = fakeClient([{ status: 'access_denied' }]);
    const err = await runDeviceFlow(client, { onCode: () => {}, sleep: async () => {} }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(DeviceFlowError);
    expect((err as DeviceFlowError).reason).toBe('denied');
  });

  it('fails with "expired" when the server reports expired_token', async () => {
    const { client } = fakeClient([{ status: 'authorization_pending' }, { status: 'expired_token' }]);
    const err = await runDeviceFlow(client, { onCode: () => {}, sleep: async () => {} }).catch((e: unknown) => e);
    expect((err as DeviceFlowError).reason).toBe('expired');
  });

  it('treats an approved response without tokens as expired', async () => {
    const { client } = fakeClient([{ status: 'approved', access_token: null }]);
    const err = await runDeviceFlow(client, { onCode: () => {}, sleep: async () => {} }).catch((e: unknown) => e);
    expect((err as DeviceFlowError).reason).toBe('expired');
  });

  it('stops polling when cancelled', async () => {
    const abort = new AbortController();
    const { client, pollDeviceToken } = fakeClient([{ status: 'authorization_pending' }]);
    const sleep = async () => {
      abort.abort();
    };
    const err = await runDeviceFlow(client, { onCode: () => {}, sleep, signal: abort.signal }).catch((e: unknown) => e);
    expect((err as DeviceFlowError).reason).toBe('cancelled');
    expect(pollDeviceToken).not.toHaveBeenCalled();
  });

  it('gives up after the poll budget', async () => {
    const { client } = fakeClient(Array.from({ length: 3 }, () => ({ status: 'authorization_pending' as const })));
    const err = await runDeviceFlow(client, { onCode: () => {}, sleep: async () => {}, maxPolls: 3 }).catch(
      (e: unknown) => e,
    );
    expect((err as DeviceFlowError).reason).toBe('expired');
  });

  it('ignores a code returned after cancellation', async () => {
    const abort = new AbortController();
    const { client, setTokens } = fakeClient([]);
    vi.mocked(client.startDeviceFlow).mockImplementation(async () => {
      abort.abort();
      return START;
    });
    const onCode = vi.fn();
    await expect(runDeviceFlow(client, { onCode, signal: abort.signal })).rejects.toMatchObject({ reason: 'cancelled' });
    expect(onCode).not.toHaveBeenCalled();
    expect(setTokens).not.toHaveBeenCalled();
  });

  it('does not install approved tokens when cancelled during the poll request', async () => {
    const abort = new AbortController();
    const { client, setTokens } = fakeClient([]);
    vi.mocked(client.pollDeviceToken).mockImplementation(async () => {
      abort.abort();
      return { status: 'approved', access_token: 'late', refresh_token: 'late', user: { id: 'u', email: 'a@b.c', name: 'A', roles: [] } };
    });
    await expect(runDeviceFlow(client, { onCode: () => {}, sleep: async () => {}, signal: abort.signal })).rejects.toMatchObject({ reason: 'cancelled' });
    expect(setTokens).not.toHaveBeenCalled();
  });
});
