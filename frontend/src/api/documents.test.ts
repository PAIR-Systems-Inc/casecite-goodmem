import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('./client', () => {
  const api = {
    request: vi.fn(),
    authFetch: vi.fn(),
    getToken: vi.fn().mockReturnValue('test-token'),
  };
  return { api, API_BASE_URL: 'http://localhost:8000/api/v1' };
});

vi.mock('../utils', () => ({
  getCookie: vi.fn().mockReturnValue('csrf-token'),
}));

import { api } from './client';
import './documents';

describe('api/documents', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (api.getToken as any).mockReturnValue('test-token');
  });

  it('registers expected methods on api object', () => {
    expect(typeof (api as any).uploadDocument).toBe('function');
    expect(typeof (api as any).getDocuments).toBe('function');
    expect(typeof (api as any).getDocumentTree).toBe('function');
    expect(typeof (api as any).getDocumentContent).toBe('function');
    expect(typeof (api as any).deleteDocument).toBe('function');
  });

  it('uploadDocument posts FormData to /documents via api.request', async () => {
    const file = new File(['content'], 'test.pdf', { type: 'application/pdf' });
    (api.request as any).mockResolvedValue({ id: 'doc1', name: 'test.pdf' });

    const result = await (api as any).uploadDocument(file);
    expect(api.request).toHaveBeenCalledWith(
      '/documents',
      expect.objectContaining({ method: 'POST' }),
    );
    const [, options] = (api.request as any).mock.calls[0];
    expect(options.body).toBeInstanceOf(FormData);
    expect(options.body.get('file')).toBe(file);
    expect(result.id).toBe('doc1');
  });

  it('uploadDocument propagates errors from api.request', async () => {
    const file = new File(['content'], 'test.pdf');
    (api.request as any).mockRejectedValue(new Error('Invalid file'));

    await expect((api as any).uploadDocument(file)).rejects.toThrow('Invalid file');
  });

  it('getDocuments calls GET /documents with defaults', async () => {
    (api.request as any).mockResolvedValue({ documents: [] });
    await (api as any).getDocuments();
    expect(api.request).toHaveBeenCalledWith('/documents?limit=50&offset=0');
  });

  it('getDocuments passes custom limit and offset', async () => {
    (api.request as any).mockResolvedValue({ documents: [] });
    await (api as any).getDocuments(10, 20);
    expect(api.request).toHaveBeenCalledWith('/documents?limit=10&offset=20');
  });

  it('getDocumentTree calls GET /documents/tree', async () => {
    (api.request as any).mockResolvedValue({ tree: [] });
    await (api as any).getDocumentTree();
    expect(api.request).toHaveBeenCalledWith('/documents/tree');
  });

  it('getDocumentContent calls GET /documents/:id/content', async () => {
    (api.request as any).mockResolvedValue({ text: 'content' });
    await (api as any).getDocumentContent('doc1');
    expect(api.request).toHaveBeenCalledWith('/documents/doc1/content');
  });

  it('deleteDocument calls DELETE /documents/:id', async () => {
    (api.request as any).mockResolvedValue({ message: 'deleted' });
    await (api as any).deleteDocument('doc1');
    expect(api.request).toHaveBeenCalledWith('/documents/doc1', { method: 'DELETE' });
  });

  it('getAllDocuments walks every page by total, 200 at a time', async () => {
    (api.request as any)
      .mockResolvedValueOnce({ documents: [{ id: 'a' }, { id: 'b' }], count: 2, total: 3 })
      .mockResolvedValueOnce({ documents: [{ id: 'c' }], count: 1, total: 3 });
    const docs = await (api as any).getAllDocuments();
    expect(docs.map((d: { id: string }) => d.id)).toEqual(['a', 'b', 'c']);
    expect(api.request).toHaveBeenNthCalledWith(1, '/documents?limit=200&offset=0');
    expect(api.request).toHaveBeenNthCalledWith(2, '/documents?limit=200&offset=2');
    expect(api.request).toHaveBeenCalledTimes(2);
  });

  it('getAllDocuments stops on an empty page and tolerates a missing total', async () => {
    (api.request as any)
      .mockResolvedValueOnce({ documents: [{ id: 'a' }] })
      .mockResolvedValueOnce({ documents: [] });
    const docs = await (api as any).getAllDocuments();
    expect(docs).toEqual([{ id: 'a' }]);
    expect(api.request).toHaveBeenCalledTimes(1);
  });
});
