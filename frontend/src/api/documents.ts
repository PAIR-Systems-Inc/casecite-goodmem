import { api } from './client';
import type { DocumentContent, DocumentInfo, DocumentListResponse, DocumentTree, MessageResponse } from './types';

Object.assign(api, {
  async uploadDocument(file: File): Promise<DocumentInfo> {
    const formData = new FormData();
    formData.append('file', file);

    // GoodMem accepts the original before ingestion finishes. Preserve the
    // original UI's upload contract: resolve only with a terminal status, so
    // every upload consumer avoids declaring a processing file "indexed".
    let document: DocumentInfo = await api.request('/documents', { method: 'POST', body: formData, timeout: 300000 });
    const deadline = Date.now() + 300000;
    while (['pending', 'processing', 'upload_uncertain'].includes(String(document.status))) {
      if (Date.now() >= deadline) {
        throw new Error('The file was received and is still processing. Check its status in the Knowledge Base before uploading it again.');
      }
      await new Promise((resolve) => setTimeout(resolve, 1000));
      document = await api.request(`/documents/${encodeURIComponent(document.id)}`);
    }
    return document;
  },

  async getDocuments(limit: number = 50, offset: number = 0): Promise<DocumentListResponse> {
    return api.request(`/documents?limit=${limit}&offset=${offset}`);
  },

  // The list endpoint pages (50 by default), so every "show the whole
  // Knowledge Base" caller walks the pages by `total` — otherwise the UI
  // silently showed only the newest page.
  async getAllDocuments(): Promise<DocumentListResponse['documents']> {
    const pageSize = 200;
    const documents: DocumentListResponse['documents'] = [];
    let offset = 0;
    let total = Infinity;
    while (offset < total) {
      const page = await api.getDocuments(pageSize, offset);
      const docs = page.documents || [];
      documents.push(...docs);
      total = typeof page.total === 'number' ? page.total : documents.length;
      if (docs.length === 0) break;
      offset += docs.length;
    }
    return documents;
  },

  async getDocumentTree(): Promise<DocumentTree> {
    return api.request('/documents/tree');
  },

  async getDocumentContent(documentId: string): Promise<DocumentContent> {
    return api.request(`/documents/${documentId}/content`);
  },

  async deleteDocument(documentId: string): Promise<MessageResponse> {
    return api.request(`/documents/${documentId}`, { method: 'DELETE' });
  },

  // ── Knowledge-base folders ──────────────────────────────────────
  async listFolders(): Promise<{ folders: string[] }> {
    return api.request('/documents/folders');
  },

  async createFolder(name: string): Promise<{ path: string; folders: string[] }> {
    return api.request('/documents/folders', {
      method: 'POST',
      body: JSON.stringify({ name }),
    });
  },

  async deleteFolder(path: string): Promise<{ status: string; folders: string[] }> {
    return api.request(`/documents/folders?path=${encodeURIComponent(path)}`, { method: 'DELETE' });
  },

  async moveDocument(documentId: string, folderPath: string | null): Promise<{ status: string }> {
    return api.request(`/documents/${documentId}/move`, {
      method: 'POST',
      body: JSON.stringify({ folder_path: folderPath }),
    });
  },
});
