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
import './matters';

const a = api as any;

describe('api/matters', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (api.request as any).mockResolvedValue({});
  });

  it('registers the matter methods on the api object', () => {
    for (const name of [
      'listMatters', 'createMatter', 'getMatter', 'updateMatter', 'deleteMatter',
      'addMatterMember', 'removeMatterMember', 'transferMatter',
      'getMatterDocuments', 'moveDocumentsToMatter', 'getMatterChats',
      'getMatterAnalyses', 'getMatterActivity',
    ]) {
      expect(typeof a[name]).toBe('function');
    }
  });

  it('createMatter posts the name and a null client when none is given', async () => {
    await a.createMatter('Acme v. Beta');
    expect(api.request).toHaveBeenCalledWith('/matters', {
      method: 'POST',
      body: JSON.stringify({ name: 'Acme v. Beta', client_name: null }),
    });
  });

  it('updateMatter PATCHes only the given fields', async () => {
    await a.updateMatter('m1', { name: 'Renamed' });
    expect(api.request).toHaveBeenCalledWith('/matters/m1', {
      method: 'PATCH',
      body: JSON.stringify({ name: 'Renamed' }),
    });
  });

  it('transferMatter posts the new owner id', async () => {
    await a.transferMatter('m1', 'user-2');
    expect(api.request).toHaveBeenCalledWith('/matters/m1/transfer', {
      method: 'POST',
      body: JSON.stringify({ new_owner_id: 'user-2' }),
    });
  });

  it('moveDocumentsToMatter posts the document ids to the destination matter', async () => {
    await a.moveDocumentsToMatter('m1', ['d1', 'd2']);
    expect(api.request).toHaveBeenCalledWith('/matters/m1/documents/move', {
      method: 'POST',
      body: JSON.stringify({ document_ids: ['d1', 'd2'] }),
    });
  });

  it('removeMatterMember DELETEs the member and encodes ids', async () => {
    await a.removeMatterMember('m 1', 'u/2');
    expect(api.request).toHaveBeenCalledWith('/matters/m%201/members/u%2F2', { method: 'DELETE' });
  });

  it('the workspace lists hit their own endpoints', async () => {
    await a.getMatterDocuments('m1');
    await a.getMatterChats('m1');
    await a.getMatterAnalyses('m1');
    await a.getMatterActivity('m1', 20);
    expect(api.request).toHaveBeenNthCalledWith(1, '/matters/m1/documents');
    expect(api.request).toHaveBeenNthCalledWith(2, '/matters/m1/chats');
    expect(api.request).toHaveBeenNthCalledWith(3, '/matters/m1/analyses');
    expect(api.request).toHaveBeenNthCalledWith(4, '/matters/m1/activity?limit=20');
  });
});
