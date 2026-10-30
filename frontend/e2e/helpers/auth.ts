import { expect, type Page, type Route } from '@playwright/test';

/**
 * Backend mocks for the e2e suite. There is no API server in CI, so every
 * call the app makes is answered here via page.route(...).fulfill(...).
 *
 * The dev build talks to http://localhost:8000/api/v1 (src/api/client.ts), a
 * different origin from the Vite server, so every reply carries CORS headers
 * and OPTIONS preflights are answered too.
 */
export const API_ROUTE = /\/\/[^/]+\/api\/|localhost:8000|\/health(?:\?|$)/;

export const TEST_USER = {
  id: 'user-1',
  email: 'jane.doe@example.com',
  name: 'Jane Doe',
  roles: ['user'],
};
/** What the header renders for the signed-in user (AppHeader: email local part). */
export const TEST_USER_HANDLE = TEST_USER.email.split('@')[0];

export interface MockDocument {
  id: string;
  filename: string;
  content_type: string;
  size: number;
  source: string;
  status: 'pending' | 'processing' | 'indexed' | 'failed';
  chunk_count: number;
  created_at: string;
  folder_path: string | null;
  metadata?: Record<string, unknown>;
}

export interface MockApiState {
  /** Whether GET /auth/me answers with the user (cookie session) or 401. */
  signedIn: boolean;
  /** What GET /documents lists; POST /documents appends to it. */
  documents: MockDocument[];
  /** Every request that reached the mock, as "METHOD /path" — for debugging. */
  calls: string[];
}

/** Handles a request itself and returns true, or returns false to fall through. */
export type MockOverride = (route: Route, url: URL, method: string, state: MockApiState) => Promise<boolean> | boolean;

export interface MockApiOptions {
  signedIn?: boolean;
  documents?: MockDocument[];
  /** Consulted first; the defaults below answer whatever they decline. */
  overrides?: MockOverride[];
}

function corsHeaders(route: Route): Record<string, string> {
  return {
    'Access-Control-Allow-Origin': route.request().headers()['origin'] ?? '*',
    'Access-Control-Allow-Credentials': 'true',
    'Access-Control-Allow-Methods': 'GET,POST,PUT,PATCH,DELETE,OPTIONS',
    'Access-Control-Allow-Headers': 'Content-Type, Authorization, X-CSRF-Token',
  };
}

/** Fulfill a routed request with a JSON body plus the CORS headers the dev origin needs. */
export function fulfillJson(route: Route, status: number, body: unknown) {
  return route.fulfill({
    status,
    headers: { ...corsHeaders(route), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
}

/** Strip the `/api/v1` prefix so handlers match on the endpoint path alone. */
function endpointOf(url: URL): string {
  return url.pathname.replace(/^\/api\/v1/, '');
}

/**
 * Install the mock backend. Returns the mutable state so a spec can flip the
 * session or inspect which endpoints were hit.
 */
export async function installApiMocks(page: Page, options: MockApiOptions = {}): Promise<MockApiState> {
  const state: MockApiState = {
    signedIn: options.signedIn ?? false,
    documents: options.documents ?? [],
    calls: [],
  };
  const overrides = options.overrides ?? [];

  await page.route(API_ROUTE, async (route) => {
    const request = route.request();
    const method = request.method().toUpperCase();
    const url = new URL(request.url());
    const path = endpointOf(url);
    state.calls.push(`${method} ${path}`);

    if (method === 'OPTIONS') {
      return route.fulfill({ status: 204, headers: corsHeaders(route) });
    }
    for (const override of overrides) {
      if (await override(route, url, method, state)) return;
    }

    // ---- bootstrap (useAppEffects) ----
    if (path === '/health') return fulfillJson(route, 200, { status: 'healthy' });
    if (path === '/csrf-token') return fulfillJson(route, 200, { csrf_token: 'test-csrf' });
    if (path === '/branding') {
      return fulfillJson(route, 200, {
        firm_name: 'CaseCite', logo_url: null, primary_color: '#E5E5E5',
        secondary_color: '#0A0A0A', accent_color: '#737373', favicon_url: null, updated_at: null,
      });
    }

    // ---- auth ----
    if (path === '/auth/me') {
      return state.signedIn
        ? fulfillJson(route, 200, TEST_USER)
        : fulfillJson(route, 401, { detail: 'Not authenticated' });
    }
    if (path === '/auth/refresh') return fulfillJson(route, 401, { detail: 'No refresh token' });
    if (path === '/auth/login' && method === 'POST') {
      state.signedIn = true;
      return fulfillJson(route, 200, { access_token: 'test-access-token', user: TEST_USER });
    }
    if (path === '/auth/logout') {
      state.signedIn = false;
      return fulfillJson(route, 200, { message: 'Logged out' });
    }

    // ---- per-sign-in data ----
    if (path === '/settings/rag') return fulfillJson(route, 200, {});
    if (path === '/connectors') return fulfillJson(route, 200, { connectors: [] });
    if (path === '/pickers/config') return fulfillJson(route, 200, {});
    if (path === '/chat/sessions') return fulfillJson(route, 200, { sessions: [] });
    if (path === '/matters' && method === 'GET') return fulfillJson(route, 200, { matters: [] });
    if (path === '/workspace-sessions' && method === 'GET') return fulfillJson(route, 200, { sessions: [] });
    if (path === '/workspace-sessions' && method === 'POST') {
      return fulfillJson(route, 200, { id: 'ws-1', surface: 'tool', title: 'session', updated_at: new Date().toISOString() });
    }
    if (/^\/workspace-sessions\/[^/]+$/.test(path) && method === 'PUT') {
      return fulfillJson(route, 200, { id: path.split('/').pop(), surface: 'tool', title: 'session', updated_at: new Date().toISOString() });
    }

    // ---- documents ----
    if (path === '/documents/folders') return fulfillJson(route, 200, { folders: [] });
    if (path === '/documents/tree') return fulfillJson(route, 200, { tree: [], total_documents: state.documents.length });
    if (path === '/documents' && method === 'GET') {
      return fulfillJson(route, 200, {
        documents: state.documents, count: state.documents.length, total: state.documents.length,
      });
    }

    // Anything else is a gap in the mock: answer loudly rather than hang.
    return fulfillJson(route, 404, { detail: `Not mocked: ${method} ${path}` });
  });

  return state;
}

export const loginModal = (page: Page) => page.locator('[class*="modalContainer"]');

/** Open Sign In from the header and submit the test user's credentials (no assertions on the outcome). */
export async function submitLogin(page: Page, email = TEST_USER.email, password = 'correct-horse-battery') {
  const header = page.locator('header[data-layout="header"]');
  await header.getByRole('button', { name: /Sign In/i }).click();
  const modal = loginModal(page);
  await expect(modal.getByRole('heading', { name: 'Sign In' })).toBeVisible();
  await modal.locator('input[type="email"]').fill(email);
  await modal.locator('input[type="password"]').fill(password);
  await modal.getByRole('button', { name: 'Sign In', exact: true }).click();
}

/** Wait until the header shows the signed-in user. */
export async function expectSignedIn(page: Page) {
  const header = page.locator('header[data-layout="header"]');
  await expect(header.getByText(TEST_USER_HANDLE, { exact: true })).toBeVisible({ timeout: 10_000 });
  await expect(header.getByRole('button', { name: 'Log out' })).toBeVisible();
}

/**
 * Install the mocks with a live cookie session and open `path` signed in —
 * the bootstrap's GET /auth/me answers with the user, exactly as it does
 * after a real login on a later visit.
 */
export async function loginAs(page: Page, path = '/', options: Omit<MockApiOptions, 'signedIn'> = {}) {
  const state = await installApiMocks(page, { ...options, signedIn: true });
  await page.goto(path);
  await expectSignedIn(page);
  return state;
}
