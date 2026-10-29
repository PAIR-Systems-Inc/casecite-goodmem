import { useId } from 'react';
import type { RagSettings } from '../../../types';

interface EmbeddingTabProps {
  local: RagSettings;
  setLocal: (s: RagSettings) => void;
  styles: Record<string, string>;
}

export const EmbeddingTab = ({ local, setLocal, styles }: EmbeddingTabProps) => {
  const id = useId();
  return (
  <div className={styles.settingsGrid}>
    <div className={styles.settingGroup}>
      <label className={styles.settingLabel} htmlFor={`${id}-embedding-model`}>Embedding Model</label>
      <select id={`${id}-embedding-model`} className={styles.select} value={local.embeddingModel} onChange={e => setLocal({...local, embeddingModel: e.target.value})}>
        <option value="text-embedding-3-large">OpenAI text-embedding-3-large (3072d)</option>
        <option value="text-embedding-3-small">OpenAI text-embedding-3-small (1536d)</option>
        <option value="voyage-law-2">Voyage AI voyage-law-2 (1024d)</option>
        <option value="cohere-embed-v3">Cohere embed-v3 (1024d)</option>
      </select>
    </div>
    <div className={styles.settingGroup}>
      <label className={styles.settingLabel} htmlFor={`${id}-dimensions`}>Embedding Dimensions</label>
      <input id={`${id}-dimensions`} type="number" className={styles.modalInput} value={local.dimensions} onChange={e => setLocal({...local, dimensions: parseInt(e.target.value)})} />
    </div>
  </div>
  );
};
