import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

vi.mock('./client', () => {
  const api = {
    request: vi.fn(),
    authFetch: vi.fn(),
  };
  return { api, API_BASE_URL: 'http://localhost:8000/api/v1' };
});

import { api } from './client';
import './settings';

describe('api/settings', () => {
  beforeEach(() => { vi.clearAllMocks(); });
  afterEach(() => { vi.useRealTimers(); });

  it('registers expected methods on api object', () => {
    expect(typeof (api as any).getSettings).toBe('function');
    expect(typeof (api as any).updateSettings).toBe('function');
  });

  it('getSettings calls GET /settings/rag', async () => {
    (api.request as any).mockResolvedValue({ model: 'gpt-5.5', temperature: 0.7 });
    const result = await (api as any).getSettings();
    expect(api.request).toHaveBeenCalledWith('/settings/rag');
    expect(result.model).toBe('gpt-5.5');
  });

  it('updateSettings calls PUT /settings/rag with settings', async () => {
    const settings = { model: 'gpt-5.5', temperature: 0.5 };
    (api.request as any).mockResolvedValue(settings);
    const result = await (api as any).updateSettings(settings);
    expect(api.request).toHaveBeenCalledWith('/settings/rag', {
      method: 'PUT',
      body: JSON.stringify(settings),
    });
    expect(result.temperature).toBe(0.5);
  });

  it('polls a durable rebuild and preserves per-document failures', async () => {
    vi.useFakeTimers();
    const result = { status: 'completed', reindexed: 2, errors: ['bad.pdf: unreadable'] };
    vi.mocked(api.request).mockResolvedValueOnce({ job_id: 'rebuild' }).mockResolvedValueOnce({ status: 'running' }).mockResolvedValueOnce({ status: 'completed', result });
    const pending = api.reindexDocuments();
    await vi.runAllTimersAsync();
    expect(await pending).toEqual(result);
    expect(api.request).toHaveBeenNthCalledWith(1, '/settings/reindex?async=true', { method: 'POST' });
    expect(api.request).toHaveBeenNthCalledWith(3, '/jobs/rebuild');
  });

  it('reports a stopped rebuild without submitting it again', async () => {
    vi.mocked(api.request).mockResolvedValueOnce({ job_id: 'rebuild' }).mockResolvedValueOnce({ status: 'cancelled', error: 'Cancelled by the user' });
    await expect(api.reindexDocuments()).rejects.toThrow('Cancelled by the user');
    expect(api.request).toHaveBeenCalledTimes(2);
  });

  it('bounds polling failures and explains how to reconnect', async () => {
    vi.useFakeTimers();
    vi.mocked(api.request).mockResolvedValueOnce({ job_id: 'rebuild' }).mockRejectedValue(new Error('Offline'));
    const pending = expect(api.reindexDocuments()).rejects.toThrow('click Rebuild to reconnect');
    await vi.runAllTimersAsync();
    await pending;
    expect(api.request).toHaveBeenCalledTimes(4);
  });
});
