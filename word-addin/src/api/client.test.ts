import { describe, it, expect, vi } from 'vitest';
import { ApiClient, ApiError, SessionExpiredError, apiBase, webOrigin } from './client';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

function makeClient(responses: Array<Response | (() => Response)>, tokens = { accessToken: 'a1', refreshToken: 'r1' }) {
  const calls: Array<{ url: string; init: RequestInit }> = [];
  const onTokens = vi.fn();
  const fetchImpl = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ url: String(url), init: init ?? {} });
    const next = responses.shift();
    if (!next) throw new Error('unexpected request');
    return typeof next === 'function' ? next() : next;
  }) as unknown as typeof fetch;
  const client = new ApiClient({ serverUrl: 'https://casecite.example.com/', tokens, onTokens, fetchImpl });
  return { client, calls, onTokens };
}

describe('apiBase / webOrigin', () => {
  it('derives the API base from a server origin or an API URL', () => {
    expect(apiBase('https://casecite.example.com')).toBe('https://casecite.example.com/api/v1');
    expect(apiBase('https://casecite.example.com/')).toBe('https://casecite.example.com/api/v1');
    expect(apiBase('https://casecite.example.com/api/v1/')).toBe('https://casecite.example.com/api/v1');
    expect(webOrigin('https://casecite.example.com/api/v1')).toBe('https://casecite.example.com');
  });
});

describe('ApiClient', () => {
  it('sends JSON with a bearer header and parses the body', async () => {
    const { client, calls } = makeClient([jsonResponse({ matters: [{ id: 'm1', name: 'Smith' }] })]);
    const matters = await client.listMatters();
    expect(matters).toEqual([{ id: 'm1', name: 'Smith' }]);
    expect(calls[0].url).toBe('https://casecite.example.com/api/v1/matters');
    expect((calls[0].init.headers as Record<string, string>).Authorization).toBe('Bearer a1');
    expect(calls[0].init.method).toBe('GET');
  });

  it('refreshes once on 401 with the body-borne refresh token and retries', async () => {
    const { client, calls, onTokens } = makeClient([
      jsonResponse({ detail: 'expired' }, 401),
      jsonResponse({ access_token: 'a2', refresh_token: 'r2', expires_in: 3600 }),
      jsonResponse({ id: 'u1', email: 'x@y.z', name: 'X', roles: [] }),
    ]);
    const me = await client.me();
    expect(me.email).toBe('x@y.z');
    expect(calls[1].url).toMatch(/\/auth\/refresh$/);
    expect(JSON.parse(calls[1].init.body as string)).toEqual({ refresh_token: 'r1' });
    expect((calls[1].init.headers as Record<string, string>).Authorization).toBeUndefined();
    expect((calls[2].init.headers as Record<string, string>).Authorization).toBe('Bearer a2');
    expect(onTokens).toHaveBeenCalledWith({ accessToken: 'a2', refreshToken: 'r2' });
  });

  it('ends the session when the refresh is refused', async () => {
    const { client, onTokens } = makeClient([
      jsonResponse({ detail: 'expired' }, 401),
      jsonResponse({ detail: 'Invalid refresh token' }, 401),
    ]);
    await expect(client.me()).rejects.toBeInstanceOf(SessionExpiredError);
    expect(client.signedIn).toBe(false);
    expect(onTokens).toHaveBeenLastCalledWith(null);
  });

  it('does not retry a 401 from an unauthenticated call', async () => {
    const { client, calls } = makeClient([jsonResponse({ detail: 'nope' }, 401)], { accessToken: '', refreshToken: '' });
    const err = await client.pollDeviceToken('d').catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).not.toBeInstanceOf(SessionExpiredError);
    expect((err as ApiError).status).toBe(401);
    expect(calls).toHaveLength(1);
  });

  it('surfaces the server detail and code on other errors', async () => {
    const { client } = makeClient([jsonResponse({ detail: 'Set up MFA first.', code: 'mfa_enrollment_required' }, 403)]);
    const err = await client.listMatters().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(403);
    expect((err as ApiError).code).toBe('mfa_enrollment_required');
    expect((err as ApiError).message).toBe('Set up MFA first.');
  });

  it('turns a network failure into an ApiError', async () => {
    const fetchImpl = vi.fn(async () => {
      throw new TypeError('Failed to fetch');
    }) as unknown as typeof fetch;
    const client = new ApiClient({ serverUrl: 'https://x.test', fetchImpl });
    const err = await client.startDeviceFlow().catch((e: unknown) => e);
    expect((err as ApiError).code).toBe('network');
  });

  it('analyzeText and research post the shapes the API expects', async () => {
    const { client, calls } = makeClient([
      jsonResponse({ analysis_id: 'an1', contract_type: 'nda', key_terms: {}, issues: [] }),
      jsonResponse({ id: 'c1', content: 'Answer', citations: [] }),
      jsonResponse({ id: 'c2', content: 'Answer', citations: [] }),
    ]);
    await client.analyzeText('Some contract', 'Customer');
    expect(JSON.parse(calls[0].init.body as string)).toEqual({
      document_text: 'Some contract',
      contract_type: 'auto',
      representing: 'Customer',
    });
    await client.research('What is the cap?', ['d1', 'd2']);
    const body = JSON.parse(calls[1].init.body as string);
    expect(body.mode).toBe('research');
    expect(body.document_filter).toEqual({ document_ids: ['d1', 'd2'] });
    await client.research('What is the cap?');
    expect(JSON.parse(calls[2].init.body as string).document_filter).toBeUndefined();
  });

  it('logout drops the tokens even when the server call fails', async () => {
    const { client, onTokens } = makeClient([jsonResponse({ detail: 'boom' }, 500)]);
    await client.logout();
    expect(client.signedIn).toBe(false);
    expect(onTokens).toHaveBeenLastCalledWith(null);
  });
});
