export interface User {
  id: string;
  name: string;
  email: string;
  roles: string[];
  must_change_password: boolean;
}
export interface Matter {
  id: string;
  name: string;
  client_name: string;
  owner_id: string;
  is_owner: boolean;
}
export interface Document {
  id: string;
  user_id: string;
  matter_id: string | null;
  filename: string;
  size: number;
  status: string;
  error?: string;
  content_type: string;
}
export interface Citation {
  number: number;
  document_id?: string;
  source: string;
  passage: string;
  url?: string;
  raw_score?: number;
  score_stage?: string;
  cited_in_answer?: boolean;
  verification_scope: string;
}
export interface Finding {
  title: string;
  severity: string;
  document_id?: string;
  quote: string;
  quote_verified: boolean;
  explanation: string;
  proposed_language?: string;
  verification_scope: string;
}
export interface Cell {
  field: string;
  value: string;
  quote: string;
  quote_verified: boolean;
  review_status: string;
  review_note?: string;
}
export interface Result {
  id: string;
  kind: string;
  title: string;
  prompt?: string;
  answer: string;
  created_at?: string;
  document_ids?: string[];
  citations?: Citation[];
  findings?: Finding[];
  warnings?: string[];
  coverage?: { document_id: string; filename: string; coverage: string }[];
  rows?: { document_id: string; filename: string; cells: Cell[] }[];
  stats?: { model: string; usage?: { total_tokens?: number } };
}
export interface Job {
  job_id: string;
  kind: string;
  status: string;
  error?: string;
  result?: Result;
}
export interface Settings {
  llm_model: string;
  top_k: number;
  keys: Record<string, boolean>;
  max_upload_mb: number;
}

const base = '/api/v1';
export const url = (path: string) => base + path;
let csrfPromise: Promise<void> | undefined;
async function csrf() {
  if (document.cookie.split('; ').some((c) => c.startsWith('_csrf='))) return;
  if (!csrfPromise)
    csrfPromise = fetch(url('/csrf-token'), { credentials: 'same-origin' })
      .then((r) => {
        if (!r.ok) throw new Error('Could not establish a secure session');
      })
      .finally(() => {
        csrfPromise = undefined;
      });
  await csrfPromise;
}
export async function api<T>(path: string, method = 'GET', data?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (method !== 'GET') {
    await csrf();
    headers['X-CSRF-Token'] =
      document.cookie
        .split('; ')
        .find((c) => c.startsWith('_csrf='))
        ?.slice(6) || '';
  }
  const body = data instanceof FormData ? data : data === undefined ? undefined : JSON.stringify(data);
  if (body && !(body instanceof FormData)) headers['Content-Type'] = 'application/json';
  const r = await fetch(url(path), { method, credentials: 'same-origin', headers, body });
  if (!r.ok) {
    const e = await r.json().catch(() => ({}));
    throw new Error(e.detail || `Request failed (${r.status})`);
  }
  return r.json();
}
export const errorMessage = (e: unknown) => (e instanceof Error ? e.message : 'Something went wrong');
