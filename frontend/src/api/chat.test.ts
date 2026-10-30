import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

vi.mock('./client', () => {
  const api = {
    request: vi.fn(),
    authFetch: vi.fn(),
  };
  return { api, API_BASE_URL: 'http://localhost:8000/api/v1' };
});

import { api } from './client';
import './chat';

describe('api/chat', () => {
  beforeEach(() => { vi.clearAllMocks(); });
  afterEach(() => { vi.useRealTimers(); });

  it('registers query method on api object', () => {
    expect(typeof (api as any).query).toBe('function');
  });

  it('query sends POST to /chat with default options', async () => {
    (api.request as any).mockResolvedValueOnce({ job_id: 'job-1' }).mockResolvedValueOnce({ status: 'completed', result: { response: 'answer' } });
    await (api as any).query('What is tort law?', 'research');
    expect(api.request).toHaveBeenCalledWith('/chat?async=true', {
      method: 'POST',
      body: JSON.stringify({
        query: 'What is tort law?',
        mode: 'research',
        include_citations: true,
        // null = server decides per message whether authority helps
        include_case_law: null,
        case_law_limit: 5,
        include_documents: true,
        document_filter: null,
        session_id: null,
      }),
    });
  });

  it('query passes custom options correctly', async () => {
    (api.request as any).mockResolvedValueOnce({ job_id: 'job-1' }).mockResolvedValueOnce({ status: 'completed', result: { response: 'result' } });
    await (api as any).query('test', 'analysis', {
      includeCaseLaw: false,
      caseLawLimit: 10,
      includeDocuments: false,
      documentFilter: 'contracts',
      sessionId: 'sess-77',
    });
    expect(api.request).toHaveBeenCalledWith('/chat?async=true', {
      method: 'POST',
      body: JSON.stringify({
        query: 'test',
        mode: 'analysis',
        include_citations: true,
        include_case_law: false,
        case_law_limit: 10,
        include_documents: false,
        document_filter: 'contracts',
        session_id: 'sess-77',
      }),
    });
  });

  it('query returns the response data', async () => {
    const expected = { response: 'Legal analysis', citations: [] };
    (api.request as any).mockResolvedValueOnce({ job_id: 'job-1' }).mockResolvedValueOnce({ status: 'completed', result: expected });
    const result = await (api as any).query('test', 'research');
    expect(result).toEqual(expected);
  });

  it('polls the same durable job without resubmitting the model request', async () => {
    vi.useFakeTimers();
    vi.mocked(api.request)
      .mockResolvedValueOnce({ job_id: 'slow-1' })
      .mockResolvedValueOnce({ status: 'pending' })
      .mockResolvedValueOnce({ status: 'running' })
      .mockResolvedValueOnce({ status: 'completed', result: { content: 'Verified answer' } });
    const query = api.query('Notice?', 'research');
    await vi.advanceTimersByTimeAsync(2000);
    expect(await query).toEqual({ content: 'Verified answer' });
    expect(api.request).toHaveBeenCalledTimes(4);
    expect(vi.mocked(api.request).mock.calls.filter(([path]) => path.startsWith('/chat'))).toHaveLength(1);
  });

  it('cancels the server job when stopped while waiting between polls', async () => {
    vi.useFakeTimers();
    const control = new AbortController();
    vi.mocked(api.request).mockResolvedValueOnce({ job_id: 'cancel-1' }).mockResolvedValueOnce({ status: 'running' }).mockResolvedValueOnce({});
    const query = api.query('Notice?', 'research', {}, control.signal);
    const rejected = expect(query).rejects.toMatchObject({ name: 'AbortError' });
    await vi.advanceTimersByTimeAsync(0);
    control.abort();
    await rejected;
    expect(api.request).toHaveBeenLastCalledWith('/jobs/cancel-1', { method: 'DELETE' });
    await vi.advanceTimersByTimeAsync(5000);
    expect(api.request).toHaveBeenCalledTimes(3);
  });

  it('does not cancel an answer after it was delivered', async () => {
    const control = new AbortController();
    vi.mocked(api.request).mockResolvedValueOnce({ job_id: 'done-1' }).mockResolvedValueOnce({ status: 'completed', result: { content: 'Done' } });
    await api.query('Notice?', 'research', {}, control.signal);
    control.abort();
    expect(api.request).toHaveBeenCalledTimes(2);
  });

  it('reports the server failure and does not retry generation', async () => {
    vi.mocked(api.request).mockResolvedValueOnce({ job_id: 'failed-1' }).mockResolvedValueOnce({ status: 'failed', error: 'The selected document changed.' });
    await expect(api.query('Notice?', 'research')).rejects.toThrow('The selected document changed.');
    expect(api.request).toHaveBeenCalledTimes(2);
  });

  it('retries a failed status read while preserving the existing job', async () => {
    vi.useFakeTimers();
    vi.mocked(api.request).mockResolvedValueOnce({ job_id: 'network-1' }).mockRejectedValueOnce(new Error('Network disconnected')).mockResolvedValueOnce({ status: 'completed', result: { content: 'Recovered' } });
    const query = api.query('Notice?', 'research');
    await vi.advanceTimersByTimeAsync(1000);
    expect(await query).toEqual({ content: 'Recovered' });
    expect(api.request).toHaveBeenCalledTimes(3);
  });
});
