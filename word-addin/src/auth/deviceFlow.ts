import type { ApiClient, Tokens } from '../api/client';
import type { DeviceStart, UserInfo } from '../api/types';

/**
 * The device-code sign-in as the pane runs it: ask for a code, show it, poll
 * until the web app approves it. Pure apart from the client and the injected
 * `sleep`, so the state machine is unit-testable with fake timers.
 */

export type DeviceFlowFailure = 'expired' | 'denied' | 'cancelled';

export class DeviceFlowError extends Error {
  reason: DeviceFlowFailure;
  constructor(reason: DeviceFlowFailure) {
    super(
      reason === 'expired'
        ? 'The code expired before it was approved. Start again to get a new one.'
        : reason === 'denied'
          ? 'The sign-in was denied in CaseCite.'
          : 'Sign-in cancelled.',
    );
    this.name = 'DeviceFlowError';
    this.reason = reason;
  }
}

export interface DeviceFlowResult {
  tokens: Tokens;
  user: UserInfo;
}

export interface DeviceFlowOptions {
  /** Called once the code exists, so the UI can show it and open the approval page. */
  onCode: (start: DeviceStart) => void;
  signal?: AbortSignal;
  sleep?: (ms: number) => Promise<void>;
  /** Safety net on top of the server's expiry (default 15 minutes of polling). */
  maxPolls?: number;
}

const defaultSleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

/** The next wait: the server's interval, stretched by 5 s on every slow_down (RFC 8628 §3.5). */
export function nextInterval(current: number, status: string): number {
  return status === 'slow_down' ? current + 5 : current;
}

export async function runDeviceFlow(client: ApiClient, options: DeviceFlowOptions): Promise<DeviceFlowResult> {
  const sleep = options.sleep ?? defaultSleep;
  const start = await client.startDeviceFlow();
  if (options.signal?.aborted) throw new DeviceFlowError('cancelled');
  options.onCode(start);

  let interval = Math.max(1, start.interval || 5);
  const maxPolls = options.maxPolls ?? 180;
  for (let polls = 0; polls < maxPolls; polls += 1) {
    if (options.signal?.aborted) throw new DeviceFlowError('cancelled');
    await sleep(interval * 1000);
    if (options.signal?.aborted) throw new DeviceFlowError('cancelled');

    const result = await client.pollDeviceToken(start.device_code);
    if (options.signal?.aborted) throw new DeviceFlowError('cancelled');
    switch (result.status) {
      case 'approved': {
        if (!result.access_token || !result.refresh_token || !result.user) {
          throw new DeviceFlowError('expired');
        }
        const tokens = { accessToken: result.access_token, refreshToken: result.refresh_token };
        client.setTokens(tokens);
        return { tokens, user: result.user };
      }
      case 'access_denied':
        throw new DeviceFlowError('denied');
      case 'expired_token':
        throw new DeviceFlowError('expired');
      case 'slow_down':
      case 'authorization_pending':
      default:
        interval = nextInterval(interval, result.status);
    }
  }
  throw new DeviceFlowError('expired');
}
