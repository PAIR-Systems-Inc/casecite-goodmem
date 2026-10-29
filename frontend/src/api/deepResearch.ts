import { api } from './client';
import type {
  DeepResearchJob,
  DeepResearchPlanInput,
  DeepResearchPlanResponse,
  DeepResearchRunDetail,
  DeepResearchRunInput,
  DeepResearchRunStarted,
  DeepResearchRunSummary,
} from './types';

const runPath = (runId: string) => `/deep-research/${encodeURIComponent(runId)}`;

const planBody = (input: DeepResearchPlanInput) => ({
  question: input.question,
  matter_id: input.matter_id || null,
  document_filter: input.document_filter || null,
});

Object.assign(api, {
  /** Propose the sub-questions and tools for a question. Nothing is searched yet. */
  async planDeepResearch(input: DeepResearchPlanInput): Promise<DeepResearchPlanResponse> {
    return api.request('/deep-research/plan', { method: 'POST', body: JSON.stringify(planBody(input)) });
  },

  /** Persist the approved plan and run it as one background job (202 + a job to poll). */
  async runDeepResearch(input: DeepResearchRunInput): Promise<DeepResearchRunStarted> {
    return api.request('/deep-research/run', {
      method: 'POST',
      body: JSON.stringify({ ...planBody(input), plan: input.plan }),
    });
  },

  async listDeepResearch(matterId?: string | null, limit = 20): Promise<{ runs: DeepResearchRunSummary[] }> {
    const params = new URLSearchParams({ limit: String(limit) });
    if (matterId) params.set('matter_id', matterId);
    return api.request(`/deep-research?${params.toString()}`);
  },

  async getDeepResearch(runId: string): Promise<DeepResearchRunDetail> {
    return api.request(runPath(runId));
  },

  async deleteDeepResearch(runId: string): Promise<{ status: string; id: string }> {
    return api.request(runPath(runId), { method: 'DELETE' });
  },

  async getDeepResearchJob(jobId: string): Promise<DeepResearchJob> {
    return api.request(`/jobs/${encodeURIComponent(jobId)}`);
  },

  async cancelDeepResearchJob(jobId: string): Promise<{ status: string; job_id: string }> {
    return api.request(`/jobs/${encodeURIComponent(jobId)}`, { method: 'DELETE' });
  },
});
