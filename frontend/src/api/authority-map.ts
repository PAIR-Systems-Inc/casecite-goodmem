import { api } from './client';
import type { AuthorityMapJob, AuthorityMapPayload, AuthorityMapResult } from './types';

Object.assign(api, {
  async submitAuthorityMap(payload: AuthorityMapPayload): Promise<{ job_id: string }> {
    return api.request('/authority-map/analyze', {
      method: 'POST',
      body: JSON.stringify({
        document_text: payload.documentText,
        document_name: payload.documentName ?? null,
        jurisdiction: payload.jurisdiction ?? null,
      }),
    });
  },

  async getAuthorityMapJob(jobId: string): Promise<AuthorityMapJob> {
    return api.request(`/jobs/${jobId}`);
  },

  async getAuthorityMapRun(runId: string): Promise<AuthorityMapResult> {
    return api.request(`/authority-map/${encodeURIComponent(runId)}`);
  },

  async deleteAuthorityMap(runId: string): Promise<{ status: string; id: string }> {
    return api.request(`/authority-map/${encodeURIComponent(runId)}`, { method: 'DELETE' });
  },

  // `kind` says what the id is: 'opinion' for a citation's opinion_id,
  // 'cluster' for a search-result id. Omitted, the server tries cluster
  // first, then opinion — which can resolve an opinion id to the wrong case.
  async getCaseOpinion(opinionId: string, kind?: 'opinion' | 'cluster'): Promise<{ case_name?: string; opinion_text?: string; syllabus?: string; [k: string]: unknown }> {
    const query = kind ? `?kind=${kind}` : '';
    return api.request(`/tools/cases/${opinionId}${query}`);
  },
});
