import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('./client', () => {
  const api = {
    request: vi.fn(),
    authFetch: vi.fn(),
    getToken: vi.fn().mockReturnValue('test-token'),
  };
  return { api, API_BASE_URL: 'http://localhost:8000/api/v1' };
});

import { api } from './client';
import './deepResearch';

const a = api as any;
const PLAN = [{ id: 'q1', question: 'What does the MSA say?', tools: ['documents'] }];

describe('api/deepResearch', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (api.request as any).mockResolvedValue({});
  });

  it('registers the deep-research methods on the api object', () => {
    for (const name of [
      'planDeepResearch', 'runDeepResearch', 'listDeepResearch', 'getDeepResearch',
      'deleteDeepResearch', 'getDeepResearchJob', 'cancelDeepResearchJob',
    ]) {
      expect(typeof a[name]).toBe('function');
    }
  });

  it('planDeepResearch normalises the optional scope to null', async () => {
    await a.planDeepResearch({ question: 'Is the cap enforceable?' });
    expect(api.request).toHaveBeenCalledWith('/deep-research/plan', {
      method: 'POST',
      body: JSON.stringify({ question: 'Is the cap enforceable?', matter_id: null, document_filter: null }),
    });
  });

  it('runDeepResearch sends the matter, the pick and the plan', async () => {
    await a.runDeepResearch({
      question: 'Q?', matter_id: 'm1', document_filter: { document_ids: ['d1'] }, plan: PLAN,
    });
    expect(api.request).toHaveBeenCalledWith('/deep-research/run', {
      method: 'POST',
      body: JSON.stringify({ question: 'Q?', matter_id: 'm1', document_filter: { document_ids: ['d1'] }, plan: PLAN }),
    });
  });

  it('listDeepResearch filters by matter when one is given', async () => {
    await a.listDeepResearch();
    expect(api.request).toHaveBeenCalledWith('/deep-research?limit=20');
    await a.listDeepResearch('m1', 5);
    expect(api.request).toHaveBeenLastCalledWith('/deep-research?limit=5&matter_id=m1');
  });

  it('run detail, delete, job polling and cancel hit the right paths', async () => {
    await a.getDeepResearch('r 1');
    expect(api.request).toHaveBeenCalledWith('/deep-research/r%201');
    await a.deleteDeepResearch('r1');
    expect(api.request).toHaveBeenLastCalledWith('/deep-research/r1', { method: 'DELETE' });
    await a.getDeepResearchJob('j1');
    expect(api.request).toHaveBeenLastCalledWith('/jobs/j1');
    await a.cancelDeepResearchJob('j1');
    expect(api.request).toHaveBeenLastCalledWith('/jobs/j1', { method: 'DELETE' });
  });
});
