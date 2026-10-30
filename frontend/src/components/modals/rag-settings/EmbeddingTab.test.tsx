import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { api } from '../../../api';
import { EmbeddingTab } from './EmbeddingTab';
import type { RagSettings } from '../../../types';

vi.mock('../../../api', () => ({ api: { request: vi.fn() } }));
beforeEach(() => {
  vi.mocked(api.request).mockResolvedValue({ default_model: 'local-default', models: [{ id: 'local-law', name: 'Local legal embedding', model: 'law-model', dimensions: 768, default: false }] });
});

function defaultProps(overrides: Record<string, any> = {}) {
  const local: RagSettings = {
    vectorDb: 'chroma',
    indexName: 'default',
    embeddingModel: 'text-embedding-3-large',
    dimensions: 3072,
    chunkSize: 512,
    chunkOverlap: 64,
    similarityThreshold: 0.7,
    topK: 10,
    enableReranking: true,
    hybridSearch: true,
    citationVerification: true,
    contextCompression: false,
    queryExpansion: false,
    sourceTracking: true,
    llmModel: 'gpt-5.5',
    temperature: 0.1,
    maxTokens: 4096,
  };
  return {
    local,
    setLocal: vi.fn(),
    styles: new Proxy({} as Record<string, string>, { get: (_, key) => String(key) }),
    ...overrides,
  };
}

describe('EmbeddingTab', () => {
  it('renders without crashing', () => {
    const { container } = render(<EmbeddingTab {...defaultProps()} />);
    expect(container).toBeTruthy();
  });

  it('shows the embedding model selector', () => {
    const { container } = render(<EmbeddingTab {...defaultProps()} />);
    expect(container.textContent).toContain('Embedding Model');
  });

  it('shows configured dimensions and applies the selected document model separately', async () => {
    const props = defaultProps();
    render(<EmbeddingTab {...props} />);
    expect(await screen.findByRole('option', { name: 'Local legal embedding (768d)' })).toBeTruthy();
    fireEvent.change(screen.getByLabelText('Document Embedding Model'), { target: { value: 'local-law' } });
    expect(props.setLocal).toHaveBeenCalledWith({ ...props.local, documentEmbedderId: 'local-law' });
  });

  it('does not offer a batch size the server has no setting for', () => {
    const { container } = render(<EmbeddingTab {...defaultProps()} />);
    expect(container.textContent).not.toContain('Batch Size');
  });

  it('has a select element for embedding model', () => {
    const { container } = render(<EmbeddingTab {...defaultProps()} />);
    const selects = container.querySelectorAll('select');
    expect(selects.length).toBe(2);
  });

  it('lists model options', () => {
    const { container } = render(<EmbeddingTab {...defaultProps()} />);
    expect(container.textContent).toContain('voyage-law-2');
    expect(container.textContent).toContain('text-embedding-3-large');
  });

  it('keeps the existing selection and allows retry when the catalog fails', async () => {
    vi.mocked(api.request).mockRejectedValueOnce(new Error('GoodMem unavailable'));
    const props = defaultProps();
    render(<EmbeddingTab {...props} />);
    expect(await screen.findByRole('alert')).toHaveTextContent('GoodMem unavailable');
    expect(props.setLocal).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('option', { name: 'Local legal embedding (768d)' })).toBeTruthy();
  });
});
