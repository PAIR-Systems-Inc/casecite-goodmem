import { api } from './client';
import type { PickerConfig, ImportResponse, BoxPickerToken } from './types';

async function importFiles(provider: string, files: unknown[]): Promise<ImportResponse> {
  const started: { job_id: string } = await api.request(`/pickers/${provider}/import?async=true`, {
    method: 'POST', body: JSON.stringify(files),
  });
  if (!started.job_id) throw new Error('The import did not return a tracking ID.');
  const path = `/jobs/${encodeURIComponent(started.job_id)}`;
  let failures = 0;
  for (;;) {
    let job: { status: string; result?: ImportResponse; error?: string };
    try {
      job = await api.request(path);
      failures = 0;
    } catch (error) {
      if (++failures > 2) throw new Error(`Could not check the import. Files may still appear in the Knowledge Base. ${error instanceof Error ? error.message : ''}`.trim());
      await new Promise(resolve => setTimeout(resolve, 1000));
      continue;
    }
    if (job.status === 'completed') {
      if (!job.result) throw new Error('The completed import has no result. Check the Knowledge Base.');
      return job.result;
    }
    if (job.status === 'failed' || job.status === 'cancelled') {
      throw new Error(job.error || 'Import stopped. Any files already indexed remain in the Knowledge Base.');
    }
    await new Promise(resolve => setTimeout(resolve, 1000));
  }
}

Object.assign(api, {
  async getPickerConfig(): Promise<PickerConfig> {
    return api.request('/pickers/config');
  },

  async getBoxPickerToken(): Promise<BoxPickerToken> {
    return api.request('/pickers/box/token');
  },

  async importFromGooglePicker(files: unknown[]): Promise<ImportResponse> {
    return importFiles('google', files);
  },

  async importFromOneDrivePicker(files: unknown[]): Promise<ImportResponse> {
    return importFiles('microsoft', files);
  },

  async importFromBoxPicker(files: unknown[]): Promise<ImportResponse> {
    return importFiles('box', files);
  },

  async importFromDropboxChooser(
    files: Array<{ name: string; url?: string; size?: number }>,
  ): Promise<ImportResponse> {
    // The backend takes Dropbox Chooser's own field names (`link`, `bytes`).
    const body = files.map(f => ({ name: f.name, link: f.url, bytes: f.size }));
    return importFiles('dropbox', body);
  },
});
