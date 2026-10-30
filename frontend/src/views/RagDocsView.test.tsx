import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { useRagDocsStore } from '../stores/ragDocsStore';
import { useAuthStore } from '../stores/authStore';
import { api } from '../api';
import type { UserInfo } from '../api/types';
import type { RagDocument } from '../types';

// Mock the api module
vi.mock('../api', () => ({
  api: {
    getDocuments: vi.fn().mockResolvedValue({ documents: [] }),
    getAllDocuments: vi.fn().mockResolvedValue([]),
    listFolders: vi.fn().mockResolvedValue({ folders: [] }),
    request: vi.fn().mockResolvedValue({}),
    retryDocumentOCR: vi.fn().mockResolvedValue({ job_id: 'retry', status: 'pending' }),
    retryDocumentIndex: vi.fn().mockResolvedValue({ job_id: 'retry', status: 'pending' }),
  },
  API_BASE_URL: 'http://localhost:8000/api/v1',
}));

import RagDocsView from './RagDocsView';

// RagDocsView reads state from the rag docs / settings / connectors stores.
describe('RagDocsView', () => {
  beforeEach(() => {
    useRagDocsStore.setState({
      ragDocs: [],
      ragDocsLoading: false,
      ragDocsSearchQuery: '',
      folders: [],
    });
  });

  it('renders without crashing', () => {
    const { container } = render(<RagDocsView />);
    expect(container).toBeTruthy();
  });

  it('shows the Knowledge Base heading', () => {
    render(<RagDocsView />);
    expect(screen.getByText('Knowledge Base')).toBeInTheDocument();
  });

  it('shows OCR failure details and allows the owner to retry', async () => {
    useAuthStore.setState({ user: { id: 'owner', roles: ['attorney'] } as UserInfo });
    const doc = { id: 'scan', filename: 'scan.pdf', status: 'failed', ocr_required: true, user_id: 'owner', error: 'GoodMem OCR did not complete pages 2.' } as RagDocument;
    useRagDocsStore.setState({ _initialized: true, ragDocs: [doc], ragDocsFiltered: [doc] });
    render(<RagDocsView />);
    expect(screen.getByText('GoodMem OCR did not complete pages 2.')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Retry OCR for scan.pdf' }));
    await waitFor(() => expect(api.retryDocumentOCR).toHaveBeenCalledWith('scan'));
    useAuthStore.setState({ user: null });
  });

  it('retries a native text index without describing it as OCR', async () => {
    useAuthStore.setState({ user: { id: 'owner', roles: ['attorney'] } as UserInfo });
    const doc = { id: 'legacy', filename: 'legacy.xls', status: 'failed', text_index_required: true, user_id: 'owner', error: 'Embedding service unavailable.' } as RagDocument;
    useRagDocsStore.setState({ _initialized: true, ragDocs: [doc], ragDocsFiltered: [doc] });
    render(<RagDocsView />);
    fireEvent.click(screen.getByRole('button', { name: 'Retry indexing for legacy.xls' }));
    await waitFor(() => expect(api.retryDocumentIndex).toHaveBeenCalledWith('legacy'));
    expect(screen.queryByText('Retry OCR')).not.toBeInTheDocument();
    useAuthStore.setState({ user: null });
  });
});
