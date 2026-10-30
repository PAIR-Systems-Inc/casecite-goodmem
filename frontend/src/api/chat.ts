import { api, API_BASE_URL } from './client';
import type { ChatResponse, ChatQueryOptions, ConversationExportPayload } from './types';

function nextPoll(signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const aborted = () => {
      clearTimeout(timer);
      signal?.removeEventListener('abort', aborted);
      reject(new DOMException('Answer cancelled', 'AbortError'));
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', aborted);
      resolve();
    }, 1000);
    signal?.addEventListener('abort', aborted, { once: true });
    if (signal?.aborted) aborted();
  });
}

interface ChatJob {
  status: 'pending' | 'running' | 'completed' | 'failed' | 'cancelled';
  result?: ChatResponse;
  error?: string;
}

Object.assign(api, {
  async exportConversation(payload: ConversationExportPayload): Promise<Blob> {
    const resp = await api.authFetch(`${API_BASE_URL}/chat/export`, {
      method: 'POST',
      body: JSON.stringify(payload),
    });
    if (!resp.ok) {
      const detail = await resp.json().catch(() => null);
      throw new Error(typeof detail?.detail === 'string' ? detail.detail : (resp.status === 501 ? 'PDF export is not available on this server.' : `Export failed (${resp.status})`));
    }
    return resp.blob();
  },

  async query(message: string, mode: string, options: ChatQueryOptions = {}, signal?: AbortSignal): Promise<ChatResponse> {
    const started: { job_id: string } = await api.request('/chat?async=true', {
      method: 'POST',
      signal,
      body: JSON.stringify({
        query: message,
        mode: mode,
        include_citations: true,
        // null = the server decides per message whether authority helps.
        include_case_law: options.includeCaseLaw ?? null,
        case_law_limit: options.caseLawLimit || 5,
        include_documents: options.includeDocuments !== false,
        document_filter: options.documentFilter || null,
        // Persisted session to append to; null lets the server start a new one.
        session_id: options.sessionId || null,
      }),
    });
    const path = `/jobs/${encodeURIComponent(started.job_id)}`;
    let cancelSent = false;
    const cancel = () => {
      if (cancelSent) return;
      cancelSent = true;
      // Use a separate request: the answer's signal is already aborted.
      void api.request(path, { method: 'DELETE' }).catch(() => {});
    };
    signal?.addEventListener('abort', cancel, { once: true });
    try {
      let pollingFailures = 0;
      while (true) {
        if (signal?.aborted) {
          cancel();
          throw new DOMException('Answer cancelled', 'AbortError');
        }
        let job: ChatJob;
        try {
          job = await api.request(path, { signal });
          pollingFailures = 0;
        } catch (error) {
          if (signal?.aborted) throw new DOMException('Answer cancelled', 'AbortError');
          if (++pollingFailures > 2) {
            throw new Error(`Could not retrieve the answer. It may still finish and appear in History. ${error instanceof Error ? error.message : ''}`.trim());
          }
          await nextPoll(signal);
          continue;
        }
        if (signal?.aborted) throw new DOMException('Answer cancelled', 'AbortError');
        if (job.status === 'completed') {
          if (!job.result) throw new Error('The completed answer is missing. Open History to check the conversation.');
          return job.result;
        }
        if (job.status === 'failed' || job.status === 'cancelled') {
          throw new Error(job.error || (job.status === 'cancelled' ? 'Answer cancelled.' : 'The answer could not be completed.'));
        }
        await nextPoll(signal);
      }
    } finally {
      signal?.removeEventListener('abort', cancel);
    }
  },
});
