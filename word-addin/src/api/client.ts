import type {
  ChatResponse,
  ContractAnalysisResult,
  DeviceStart,
  DeviceTokenResponse,
  Matter,
  MatterDocument,
  StoredSession,
  TokenRefreshResponse,
  UserInfo,
} from './types';

/**
 * The pane's HTTP client. Bearer tokens only: the pane never holds the web
 * app's cookies, which is also why the server skips CSRF for it and why the
 * refresh token travels in request and response bodies (device sessions only).
 */

export class ApiError extends Error {
  status: number;
  code?: string;
  constructor(message: string, status: number, code?: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
  }
}

/** The session is gone (refresh failed); the pane must sign in again. */
export class SessionExpiredError extends ApiError {
  constructor() {
    super('Your CaseCite session has ended. Sign in again.', 401, 'session_expired');
    this.name = 'SessionExpiredError';
  }
}

export interface Tokens {
  accessToken: string;
  refreshToken: string;
}

export interface ApiClientOptions {
  /** Server origin or API base, e.g. https://casecite.firm.com. */
  serverUrl: string;
  tokens?: Tokens | null;
  /** Called whenever the token pair changes (refresh, sign-in, sign-out). */
  onTokens?: (tokens: Tokens | null) => void;
  fetchImpl?: typeof fetch;
  /** Request timeout in milliseconds (long AI calls get their own). */
  timeoutMs?: number;
}

/** `https://host/` or `https://host/api/v1/` -> `https://host/api/v1`. */
export function apiBase(serverUrl: string): string {
  const trimmed = serverUrl.trim().replace(/\/+$/, '');
  return trimmed.endsWith('/api/v1') ? trimmed : `${trimmed}/api/v1`;
}

/** The web app origin for the same server (bundled deployments share one origin). */
export function webOrigin(serverUrl: string): string {
  return apiBase(serverUrl).replace(/\/api\/v1$/, '');
}

function detailMessage(body: unknown, fallback: string): string {
  if (body && typeof body === 'object') {
    const detail = (body as { detail?: unknown }).detail;
    if (typeof detail === 'string' && detail) return detail;
    if (Array.isArray(detail) && detail.length) {
      const first = detail[0] as { msg?: string };
      if (first && typeof first.msg === 'string') return first.msg;
    }
  }
  return fallback;
}

export class ApiClient {
  private readonly base: string;
  private tokens: Tokens | null;
  private readonly onTokens?: (tokens: Tokens | null) => void;
  private readonly fetchImpl: typeof fetch;
  private readonly timeoutMs: number;
  private refreshing: Promise<boolean> | null = null;

  constructor(options: ApiClientOptions) {
    this.base = apiBase(options.serverUrl);
    this.tokens = options.tokens ?? null;
    this.onTokens = options.onTokens;
    this.fetchImpl = options.fetchImpl ?? ((input, init) => fetch(input, init));
    this.timeoutMs = options.timeoutMs ?? 30_000;
  }

  get baseUrl(): string {
    return this.base;
  }

  get signedIn(): boolean {
    return Boolean(this.tokens?.accessToken);
  }

  setTokens(tokens: Tokens | null): void {
    this.tokens = tokens;
    this.onTokens?.(tokens);
  }

  // -- transport -----------------------------------------------------------

  private async send<T>(
    path: string,
    init: { method?: string; body?: unknown; auth?: boolean; timeoutMs?: number; retried?: boolean } = {},
  ): Promise<T> {
    const auth = init.auth !== false;
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    if (auth && this.tokens?.accessToken) headers.Authorization = `Bearer ${this.tokens.accessToken}`;

    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), init.timeoutMs ?? this.timeoutMs);
    let response: Response;
    try {
      response = await this.fetchImpl(`${this.base}${path}`, {
		credentials: 'omit',
        method: init.method ?? 'GET',
        headers,
        body: init.body === undefined ? undefined : JSON.stringify(init.body),
        signal: controller.signal,
      });
    } catch (e: unknown) {
      if (controller.signal.aborted) throw new ApiError('The request timed out. Try again.', 0, 'timeout');
      throw new ApiError(
        e instanceof Error && e.message ? `Could not reach CaseCite: ${e.message}` : 'Could not reach CaseCite.',
        0,
        'network',
      );
    } finally {
      clearTimeout(timer);
    }

    if (response.status === 401 && auth) {
      if (!init.retried && (await this.refresh())) {
        return this.send<T>(path, { ...init, retried: true });
      }
      this.setTokens(null);
      throw new SessionExpiredError();
    }

    if (!response.ok) {
      const body = await response.json().catch(() => ({}));
      const code = body && typeof body === 'object' ? (body as { code?: string }).code : undefined;
      throw new ApiError(detailMessage(body, `Request failed (${response.status})`), response.status, code);
    }
    if (response.status === 204) return undefined as T;
    return (await response.json()) as T;
  }

  /** Rotate the device refresh token. One refresh at a time; false ends the session. */
  async refresh(): Promise<boolean> {
    if (!this.tokens?.refreshToken) return false;
    if (!this.refreshing) {
      this.refreshing = (async () => {
        try {
          const data = await this.send<TokenRefreshResponse>('/auth/refresh', {
            method: 'POST',
            body: { refresh_token: this.tokens?.refreshToken },
            auth: false,
          });
          if (!data.access_token) return false;
          this.setTokens({
            accessToken: data.access_token,
            refreshToken: data.refresh_token || this.tokens?.refreshToken || '',
          });
          return true;
        } catch {
          return false;
        } finally {
          this.refreshing = null;
        }
      })();
    }
    return this.refreshing;
  }

  // -- auth ------------------------------------------------------------------

  startDeviceFlow(): Promise<DeviceStart> {
    return this.send<DeviceStart>('/auth/device/start', { method: 'POST', auth: false });
  }

  pollDeviceToken(deviceCode: string): Promise<DeviceTokenResponse> {
    return this.send<DeviceTokenResponse>('/auth/device/token', {
      method: 'POST',
      body: { device_code: deviceCode },
      auth: false,
    });
  }

  me(): Promise<UserInfo> {
    return this.send<UserInfo>('/auth/me');
  }

  /** End the server session; the local tokens are dropped either way. */
  async logout(): Promise<void> {
    try {
      if (this.tokens?.accessToken) await this.send('/auth/logout', { method: 'POST' });
    } catch {
      // Already signed out server-side, or unreachable: forgetting locally is enough.
    } finally {
      this.setTokens(null);
    }
  }

  // -- data ------------------------------------------------------------------

  async listMatters(): Promise<Matter[]> {
    const data = await this.send<{ matters: Matter[] }>('/matters');
    return data.matters ?? [];
  }

  async listMatterDocuments(matterId: string): Promise<MatterDocument[]> {
    const data = await this.send<{ documents: MatterDocument[] }>(
      `/matters/${encodeURIComponent(matterId)}/documents`,
    );
    return data.documents ?? [];
  }

  /** Full contract analysis of pasted text (the open document's body). */
  analyzeText(text: string, representing?: string): Promise<ContractAnalysisResult> {
    return this.send<ContractAnalysisResult>('/contract-analysis/analyze', {
      method: 'POST',
      body: { document_text: text, contract_type: 'auto', representing: representing || null },
      timeoutMs: 180_000,
    });
  }

  /** Research question over the firm's documents, optionally limited to some document ids. */
  research(query: string, documentIds?: string[]): Promise<ChatResponse> {
    return this.send<ChatResponse>('/chat', {
      method: 'POST',
      body: {
        query,
        mode: 'research',
        include_citations: true,
        include_documents: true,
        ...(documentIds && documentIds.length ? { document_filter: { document_ids: documentIds } } : {}),
      },
      timeoutMs: 180_000,
    });
  }
}

export function sessionFromTokens(serverUrl: string, tokens: Tokens, user: UserInfo): StoredSession {
  return { serverUrl, accessToken: tokens.accessToken, refreshToken: tokens.refreshToken, user };
}
