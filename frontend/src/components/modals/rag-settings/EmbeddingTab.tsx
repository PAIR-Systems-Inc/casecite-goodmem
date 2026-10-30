import { useEffect, useId, useState } from 'react';
import { api } from '../../../api';
import type { RagSettings } from '../../../types';

interface EmbeddingTabProps {
  local: RagSettings;
  setLocal: (s: RagSettings) => void;
  styles: Record<string, string>;
}

interface DocumentEmbedder {
  id: string;
  name: string;
  model: string;
  dimensions: number;
  default: boolean;
}

export const EmbeddingTab = ({ local, setLocal, styles }: EmbeddingTabProps) => {
  const id = useId();
  const [catalog, setCatalog] = useState<{ models: DocumentEmbedder[]; default_model: string } | null>(null);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setError('');
    api.request('/settings/document-embedders', { signal: controller.signal })
      .then(data => { if (!controller.signal.aborted) setCatalog(data); })
      .catch(err => { if (!controller.signal.aborted) setError(err instanceof Error ? err.message : 'Could not load document models.'); });
    return () => controller.abort();
  }, [attempt]);
  const selected = catalog?.models.find(model => model.id === local.documentEmbedderId);
  return (
  <div className={styles.settingsGrid}>
    <div className={styles.settingGroup}>
      <label className={styles.settingLabel} htmlFor={`${id}-document-model`}>Document Embedding Model</label>
      <select id={`${id}-document-model`} className={styles.select} value={local.documentEmbedderId ?? ''} disabled={!catalog} onChange={e => setLocal({...local, documentEmbedderId: e.target.value})}>
        <option value="">Instance default{catalog ? ` — ${catalog.default_model}` : ''}</option>
        {local.documentEmbedderId && !selected && <option value={local.documentEmbedderId} disabled>Selected model unavailable</option>}
        {catalog?.models.map(model => <option key={model.id} value={model.id}>{model.name} ({model.dimensions}d)</option>)}
      </select>
      {selected && <p className={styles.settingHint}>{selected.model} · {selected.dimensions} dimensions</p>}
      {error && <p role="alert">{error} <button type="button" onClick={() => setAttempt(attempt + 1)}>Retry</button></p>}
      {!catalog && !error && <p role="status">Loading document models…</p>}
      <p className={styles.settingHint}>Document models are configured in GoodMem. New uploads use your choice; use Rebuild in Advanced to apply it to existing documents.</p>
    </div>
    <div className={styles.settingGroup}>
      <label className={styles.settingLabel} htmlFor={`${id}-embedding-model`}>Clause Comparison Model</label>
      <select id={`${id}-embedding-model`} className={styles.select} value={local.embeddingModel} onChange={e => setLocal({...local, embeddingModel: e.target.value})}>
        <option value="text-embedding-3-large">OpenAI text-embedding-3-large (3072d)</option>
        <option value="text-embedding-3-small">OpenAI text-embedding-3-small (1536d)</option>
        <option value="voyage-law-2">Voyage AI voyage-law-2 (1024d)</option>
        <option value="embed-english-v3.0">Cohere embed-english-v3.0 (1024d)</option>
        {!['text-embedding-3-large', 'text-embedding-3-small', 'voyage-law-2', 'embed-english-v3.0'].includes(local.embeddingModel) && <option value={local.embeddingModel}>{local.embeddingModel}</option>}
      </select>
      <p className={styles.settingHint}>Used for clause comparison with your API key or the instance’s local embedding endpoint. Document search uses the GoodMem model above.</p>
    </div>
  </div>
  );
};
