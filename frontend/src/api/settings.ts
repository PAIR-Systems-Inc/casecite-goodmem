import { api } from './client';
import type { RAGSettings, ReindexResult } from './types';

Object.assign(api, {
  async getSettings(): Promise<RAGSettings> {
    return api.request('/settings/rag');
  },

  async updateSettings(settings: Record<string, unknown>): Promise<RAGSettings> {
    return api.request('/settings/rag', {
      method: 'PUT',
      body: JSON.stringify(settings),
    });
  },

  async getKeyStatus(): Promise<Record<string, boolean>> {
    return api.request('/user/keys/status');
  },

  async getMaskedKeys(): Promise<Record<string, string | null>> {
    return api.request('/user/keys/masked');
  },

  async saveApiKey(keyType: string, apiKey: string): Promise<void> {
    return api.request('/user/keys/save', {
      method: 'POST',
      body: JSON.stringify({ key_type: keyType, api_key: apiKey }),
    });
  },

  async deleteApiKey(keyType: string): Promise<void> {
    return api.request(`/user/keys/${encodeURIComponent(keyType)}`, { method: 'DELETE' });
  },

  async getPromptDefaults(): Promise<Record<string, unknown>> {
    return api.request('/settings/prompts/defaults');
  },

  async reindexDocuments(): Promise<ReindexResult> {
    const started: { job_id: string } = await api.request('/settings/reindex?async=true', { method: 'POST' });
    if (!started.job_id) throw new Error('The rebuild did not return a tracking ID.');
    let failures = 0;
    for (;;) {
      let job: { status: string; result?: ReindexResult; error?: string };
      try {
        job = await api.request(`/jobs/${encodeURIComponent(started.job_id)}`);
        failures = 0;
      } catch (error) {
        if (++failures > 2) throw new Error(`Could not check the rebuild. It may still be running; click Rebuild to reconnect. ${error instanceof Error ? error.message : ''}`.trim());
        await new Promise(resolve => setTimeout(resolve, 1000));
        continue;
      }
      if (job.status === 'completed') {
        if (!job.result) throw new Error('The completed rebuild has no result. Check the Knowledge Base.');
        return job.result;
      }
      if (job.status === 'failed' || job.status === 'cancelled') throw new Error(job.error || 'Rebuild stopped. Completed documents keep their new index.');
      await new Promise(resolve => setTimeout(resolve, 1000));
    }
  },
});
