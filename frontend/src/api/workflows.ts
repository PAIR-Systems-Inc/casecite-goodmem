import { api } from './client';
import type {
  WorkflowCreateInput,
  WorkflowDetail,
  WorkflowJob,
  WorkflowRunDetail,
  WorkflowRunStarted,
  WorkflowRunSummary,
  WorkflowSummary,
  WorkflowTemplate,
  WorkflowUpdateInput,
} from './types';

const workflowPath = (workflowId: string, suffix = '') => `/workflows/${encodeURIComponent(workflowId)}${suffix}`;

Object.assign(api, {
  async listWorkflows(): Promise<{ workflows: WorkflowSummary[] }> {
    return api.request('/workflows');
  },

  async createWorkflow(input: WorkflowCreateInput): Promise<WorkflowDetail> {
    return api.request('/workflows', {
      method: 'POST',
      body: JSON.stringify({
        name: input.name,
        description: input.description || null,
        matter_id: input.matter_id || null,
        steps: input.steps,
      }),
    });
  },

  async getWorkflow(workflowId: string): Promise<WorkflowDetail> {
    return api.request(workflowPath(workflowId));
  },

  async updateWorkflow(workflowId: string, changes: WorkflowUpdateInput): Promise<WorkflowDetail> {
    return api.request(workflowPath(workflowId), { method: 'PATCH', body: JSON.stringify(changes) });
  },

  async deleteWorkflow(workflowId: string): Promise<{ status: string; id: string }> {
    return api.request(workflowPath(workflowId), { method: 'DELETE' });
  },

  async listWorkflowTemplates(): Promise<{ templates: WorkflowTemplate[] }> {
    return api.request('/workflows/templates');
  },

  async createWorkflowFromTemplate(key: string, matterId?: string | null): Promise<WorkflowDetail> {
    const query = matterId ? `?matter_id=${encodeURIComponent(matterId)}` : '';
    return api.request(`/workflows/from-template/${encodeURIComponent(key)}${query}`, { method: 'POST' });
  },

  async runWorkflow(workflowId: string, documentIds: string[] = []): Promise<WorkflowRunStarted> {
    return api.request(workflowPath(workflowId, '/run'), {
      method: 'POST',
      body: JSON.stringify({ document_ids: documentIds }),
    });
  },

  async listWorkflowRuns(workflowId: string, limit = 50): Promise<{ runs: WorkflowRunSummary[] }> {
    return api.request(workflowPath(workflowId, `/runs?limit=${limit}`));
  },

  async getWorkflowRun(workflowId: string, runId: string): Promise<WorkflowRunDetail> {
    return api.request(workflowPath(workflowId, `/runs/${encodeURIComponent(runId)}`));
  },

  async resumeWorkflowRun(workflowId: string, runId: string): Promise<WorkflowRunStarted> {
    return api.request(workflowPath(workflowId, `/runs/${encodeURIComponent(runId)}/resume`), { method: 'POST' });
  },

  async getWorkflowJob(jobId: string): Promise<WorkflowJob> {
    return api.request(`/jobs/${jobId}`);
  },

  async cancelWorkflowJob(jobId: string): Promise<{ status: string; job_id: string }> {
    return api.request(`/jobs/${jobId}`, { method: 'DELETE' });
  },
});
