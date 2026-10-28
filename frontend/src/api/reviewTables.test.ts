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
import './reviewTables';

const a = api as any;

describe('api/reviewTables', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (api.request as any).mockResolvedValue({});
  });

  it('registers the review-table methods on the api object', () => {
    for (const name of [
      'listReviewTables', 'createReviewTable', 'getReviewTable', 'updateReviewTable', 'deleteReviewTable',
      'estimateReviewTable', 'runReviewTable', 'getReviewTableJob', 'exportReviewTable',
    ]) {
      expect(typeof a[name]).toBe('function');
    }
  });

  it('createReviewTable normalises optional fields to null / empty', async () => {
    await a.createReviewTable({ name: 'NDA sweep', fields: [{ name: 'Term', type: 'duration' }] });
    expect(api.request).toHaveBeenCalledWith('/review-tables', {
      method: 'POST',
      body: JSON.stringify({
        name: 'NDA sweep',
        description: null,
        matter_id: null,
        fields: [{ name: 'Term', type: 'duration' }],
        document_ids: [],
      }),
    });
  });

  it('updateReviewTable PATCHes only the given changes', async () => {
    await a.updateReviewTable('t1', { document_ids: ['d1', 'd2'] });
    expect(api.request).toHaveBeenCalledWith('/review-tables/t1', {
      method: 'PATCH',
      body: JSON.stringify({ document_ids: ['d1', 'd2'] }),
    });
  });

  it('runReviewTable posts the scope (or an empty object for a full run)', async () => {
    await a.runReviewTable('t1');
    expect(api.request).toHaveBeenCalledWith('/review-tables/t1/run', { method: 'POST', body: '{}' });
    await a.runReviewTable('t1', { document_ids: ['d1'], field_keys: ['term'] });
    expect(api.request).toHaveBeenLastCalledWith('/review-tables/t1/run', {
      method: 'POST',
      body: JSON.stringify({ document_ids: ['d1'], field_keys: ['term'] }),
    });
  });

  it('getReviewTableJob polls the shared jobs endpoint', async () => {
    await a.getReviewTableJob('job-9');
    expect(api.request).toHaveBeenCalledWith('/jobs/job-9');
  });

  it('encodes ids in paths', async () => {
    await a.getReviewTable('a/b');
    expect(api.request).toHaveBeenCalledWith('/review-tables/a%2Fb');
    await a.estimateReviewTable('t1');
    expect(api.request).toHaveBeenCalledWith('/review-tables/t1/estimate');
    await a.deleteReviewTable('t1');
    expect(api.request).toHaveBeenCalledWith('/review-tables/t1', { method: 'DELETE' });
  });

  it('exportReviewTable fetches the file with auth and returns the blob', async () => {
    const blob = new Blob(['x']);
    (api.authFetch as any).mockResolvedValue({ ok: true, blob: () => Promise.resolve(blob) });
    await expect(a.exportReviewTable('t1', 'csv')).resolves.toBe(blob);
    expect(api.authFetch).toHaveBeenCalledWith('http://localhost:8000/api/v1/review-tables/t1/export?format=csv');

    (api.authFetch as any).mockResolvedValue({ ok: false, status: 404 });
    await expect(a.exportReviewTable('t1', 'xlsx')).rejects.toThrow('Export failed (404)');
  });
});
