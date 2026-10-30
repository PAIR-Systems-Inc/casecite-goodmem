import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, fireEvent, act } from '@testing-library/react';
import { APIKeysTab } from './APIKeysTab';
import { api } from '../../../api/client';
import type { RagSettings } from '../../../types';
import { useUIStore } from '../../../stores/uiStore';

vi.mock('../../../api/client', () => ({
  api: { request: vi.fn().mockResolvedValue({}) },
}));

/** Answer one endpoint+method with `response`; everything else gets `{}`. */
function answer(endpoint: string, method: string, response: Record<string, unknown>) {
  (api.request as ReturnType<typeof vi.fn>).mockImplementation((ep: string, opts?: { method?: string }) =>
    Promise.resolve(ep === endpoint && (opts?.method || 'GET') === method ? response : {}));
}

function defaultProps(overrides: Record<string, any> = {}) {
  const local: RagSettings = {
    vectorDb: 'chroma',
    indexName: 'default',
    embeddingModel: 'text-embedding-3-small',
    dimensions: 1536,
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
    showKeys: {} as Record<string, boolean>,
    setShowKeys: vi.fn(),
    apiKeys: { openai: '', anthropic: '', google: '', voyage: '', cohere: '' } as Record<string, string>,
    setApiKeys: vi.fn(),
    serverKeyStatus: {} as Record<string, boolean>,
    maskedKeys: {} as Record<string, string | null>,
    keyStatusError: false,
    refreshKeyStatus: vi.fn(async () => {}),
    styles: new Proxy({} as Record<string, string>, { get: (_, key) => String(key) }),
    ...overrides,
  };
}

describe('APIKeysTab', () => {
  it('renders without crashing', () => {
    const { container } = render(<APIKeysTab {...defaultProps()} />);
    expect(container).toBeTruthy();
  });

  it('displays the Bring Your Own Keys banner', () => {
    const { container } = render(<APIKeysTab {...defaultProps()} />);
    expect(container.textContent).toContain('Bring Your Own Keys');
  });

  it('shows all provider key fields', () => {
    const { container } = render(<APIKeysTab {...defaultProps()} />);
    expect(container.textContent).toContain('OpenAI API Key');
    expect(container.textContent).toContain('Anthropic API Key');
    expect(container.textContent).toContain('Google Gemini API Key');
    expect(container.textContent).toContain('Voyage AI Key');
    expect(container.textContent).toContain('Cohere API Key');
  });

  it('shows the key status section', () => {
    const { container } = render(<APIKeysTab {...defaultProps()} />);
    expect(container.textContent).toContain('Key Status');
  });

  describe('integration saves', () => {
    afterEach(() => {
      vi.useRealTimers();
      (api.request as ReturnType<typeof vi.fn>).mockReset();
      (api.request as ReturnType<typeof vi.fn>).mockResolvedValue({});
    });

    it('marks CourtListener configured from the save response', async () => {
      vi.useFakeTimers();
      answer('/admin/integrations/courtlistener', 'POST', { status: 'saved', configured: true, source: 'instance', masked: 'abcd...wxyz' });
      const { container } = render(<APIKeysTab {...defaultProps()} />);
      await act(async () => { await vi.advanceTimersByTimeAsync(0); });
      expect(container.textContent).not.toContain('Configured (source: instance)');

      const input = container.querySelector('input[placeholder="Free Law Project API token"]') as HTMLInputElement;
      fireEvent.change(input, { target: { value: 'token-1234567890' } });
      await act(async () => { await vi.advanceTimersByTimeAsync(800); });

      expect(container.textContent).toContain('Configured (source: instance)');
      const badge = Array.from(container.querySelectorAll('div')).find((el) => el.className.includes('keyBadge') && el.textContent === 'CourtListener');
      expect(badge?.className).toContain('keyBadgeActive');
    });

    it('marks the local model configured from the save response', async () => {
      vi.useFakeTimers();
      answer('/admin/integrations/local-llm', 'POST', {
        status: 'saved', configured: true, base_url: 'http://localhost:11434/v1', chat_model: 'llama3.1:70b', utility_model: null,
      });
      const { container } = render(<APIKeysTab {...defaultProps()} />);
      await act(async () => { await vi.advanceTimersByTimeAsync(0); });

      fireEvent.change(container.querySelector('input[placeholder="http://localhost:11434/v1"]')!, { target: { value: 'http://localhost:11434/v1' } });
      fireEvent.change(container.querySelector('input[placeholder="llama3.1:70b"]')!, { target: { value: 'llama3.1:70b' } });
      await act(async () => { await vi.advanceTimersByTimeAsync(1000); });

      const badge = Array.from(container.querySelectorAll('div')).find((el) => el.className.includes('keyBadge') && el.textContent === 'Local Model');
      expect(badge?.className).toContain('keyBadgeActive');
    });

    it('cancels a pending endpoint edit when restoring deployment defaults', async () => {
      vi.useFakeTimers();
      const original = { configured: true, base_url: 'http://local:8080/v1', chat_model: 'local-model', utility_model: 'local-model' };
      (api.request as ReturnType<typeof vi.fn>).mockImplementation((ep: string) => Promise.resolve(ep.includes('local-llm') ? original : {}));
      const { getByLabelText, getByRole } = render(<APIKeysTab {...defaultProps()} />);
      await act(async () => { await vi.advanceTimersByTimeAsync(0); });
      fireEvent.change(getByLabelText('Chat Model'), { target: { value: 'pending-model' } });
      fireEvent.click(getByRole('button', { name: 'Revert to deployment defaults' }));
      await act(async () => {
        useUIStore.getState().handleConfirm();
        await vi.advanceTimersByTimeAsync(2000);
      });
      expect(getByLabelText('Chat Model')).toHaveValue('local-model');
      const writes = (api.request as ReturnType<typeof vi.fn>).mock.calls.filter(([ep, opts]) => ep.includes('local-llm') && opts?.method);
      expect(writes.map(([, opts]) => opts.method)).toEqual(['DELETE']);
    });

    it('orders a reset after an in-flight save', async () => {
      vi.useFakeTimers();
      let finishSave!: (value: Record<string, unknown>) => void;
      const methods: string[] = [];
      const original = { configured: true, base_url: 'http://local:8080/v1', chat_model: 'local-model', utility_model: 'local-model' };
      (api.request as ReturnType<typeof vi.fn>).mockImplementation((ep: string, opts?: { method?: string }) => {
        if (!ep.includes('local-llm')) return Promise.resolve({});
        if (opts?.method) methods.push(opts.method);
        if (opts?.method === 'POST') return new Promise((resolve) => { finishSave = resolve; });
        return Promise.resolve(original);
      });
      const { getByLabelText, getByRole } = render(<APIKeysTab {...defaultProps()} />);
      await act(async () => { await vi.advanceTimersByTimeAsync(0); });
      fireEvent.change(getByLabelText('Chat Model'), { target: { value: 'new-model' } });
      await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
      fireEvent.click(getByRole('button', { name: 'Revert to deployment defaults' }));
      await act(async () => { useUIStore.getState().handleConfirm(); });
      expect(methods).toEqual(['POST']);
      await act(async () => { finishSave({ ...original, chat_model: 'new-model' }); });
      expect(methods).toEqual(['POST', 'DELETE']);
      expect(getByLabelText('Chat Model')).toHaveValue('local-model');
    });
  });

  it('has password inputs for api keys', () => {
    const { container } = render(<APIKeysTab {...defaultProps()} />);
    const passwordInputs = container.querySelectorAll('input[type="password"]');
    expect(passwordInputs.length).toBeGreaterThanOrEqual(5);
  });
});
