import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

vi.mock('./client', () => ({ api: { request: vi.fn(), authFetch: vi.fn() } }));
import { api } from './client';
import './pickers';

describe('durable picker imports', () => {
  beforeEach(() => { vi.resetAllMocks(); vi.useFakeTimers(); });
  afterEach(() => { vi.useRealTimers(); });

  it.each(['google', 'microsoft', 'box', 'dropbox'])('waits for %s to finish indexing and returns exact document IDs', async provider => {
    const out = { imported: 1, failed: 0, errors: [], document_ids: ['actual-document'] };
    vi.mocked(api.request)
      .mockResolvedValueOnce({ job_id: 'job' })
      .mockResolvedValueOnce({ status: 'running', progress: { message: 'OCR in progress' } })
      .mockResolvedValueOnce({ status: 'completed', result: out });
    const methods = { google: api.importFromGooglePicker, microsoft: api.importFromOneDrivePicker, box: api.importFromBoxPicker, dropbox: api.importFromDropboxChooser };
    const files = [{ id: 'remote', name: 'notice.pdf', url: 'https://dl.dropboxusercontent.com/notice', size: 20 }];
    const result = methods[provider as keyof typeof methods](files);
    await vi.advanceTimersByTimeAsync(1000);
    expect(await result).toEqual(out);
    const payload = provider === 'dropbox' ? [{ name: 'notice.pdf', link: files[0].url, bytes: 20 }] : files;
    expect(api.request).toHaveBeenNthCalledWith(1, `/pickers/${provider}/import?async=true`, { method: 'POST', body: JSON.stringify(payload) });
    expect(api.request).toHaveBeenLastCalledWith('/jobs/job');
  });

  it('recovers transient polling errors without submitting the import again', async () => {
    const out = { imported: 0, failed: 1, errors: [{ name: 'bad.pdf', reason: 'No readable pages' }], document_ids: [] };
    vi.mocked(api.request).mockResolvedValueOnce({ job_id: 'job' }).mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({ status: 'completed', result: out });
    const result = api.importFromBoxPicker([]);
    await vi.advanceTimersByTimeAsync(1000);
    expect(await result).toEqual(out);
    expect(vi.mocked(api.request).mock.calls.filter(([, options]) => options?.method === 'POST')).toHaveLength(1);
  });

  it('reports a lost connection without implying the background import was cancelled', async () => {
    vi.mocked(api.request).mockResolvedValueOnce({ job_id: 'job' }).mockRejectedValue(new Error('offline'));
    const result = api.importFromBoxPicker([]).catch((error: Error) => error);
    await vi.advanceTimersByTimeAsync(2000);
    expect((await result as Error).message).toContain('Files may still appear in the Knowledge Base');
    expect(api.request).toHaveBeenCalledTimes(4);
  });

  it.each(['failed', 'cancelled'])('surfaces the %s job reason', async status => {
    vi.mocked(api.request).mockResolvedValueOnce({ job_id: 'job' }).mockResolvedValueOnce({ status, error: 'Provider access was revoked' });
    await expect(api.importFromGooglePicker([])).rejects.toThrow('Provider access was revoked');
  });
});
