import { useId } from 'react';
import type { RagSettings } from '../../../types';

interface RetrievalTabProps {
  local: RagSettings;
  setLocal: (s: RagSettings) => void;
  styles: Record<string, string>;
}

export const RetrievalTab = ({ local, setLocal, styles }: RetrievalTabProps) => {
  const id = useId();
  return (
  <div className={styles.settingsGrid}>
    <div className={styles.settingGroup}>
      <label className={styles.settingLabel} htmlFor={`${id}-vector-db`}>Vector Database</label>
      <select id={`${id}-vector-db`} className={styles.select} value={local.vectorDb} onChange={e => setLocal({...local, vectorDb: e.target.value})}>
        <option value="goodmem">GoodMem</option>
      </select>
    </div>
    <div className={styles.settingGroup}>
      <label className={styles.settingLabel} htmlFor={`${id}-index-name`}>Index Name</label>
      <input id={`${id}-index-name`} type="text" className={styles.modalInput} value={local.indexName} onChange={e => setLocal({...local, indexName: e.target.value})} />
    </div>
    <div className={styles.settingGroup}>
      <div className={styles.sliderHeader}>
        <label className={styles.settingLabel} htmlFor={`${id}-top-k`}>Top K Results</label>
        <span className={`${styles.sliderValue} mono`}>{local.topK}</span>
      </div>
      <input id={`${id}-top-k`} type="range" min="1" max="25" value={local.topK} onChange={e => setLocal({...local, topK: parseInt(e.target.value)})} />
    </div>
    <div className={styles.settingGroup}>
      <div className={styles.sliderHeader}>
        <label className={styles.settingLabel} htmlFor={`${id}-similarity-threshold`}>Similarity Threshold</label>
        <span className={`${styles.sliderValue} mono`}>{local.similarityThreshold.toFixed(2)}</span>
      </div>
      <input id={`${id}-similarity-threshold`} type="range" min="0.05" max="0.95" step="0.05" value={local.similarityThreshold} onChange={e => setLocal({...local, similarityThreshold: parseFloat(e.target.value)})} />
    </div>
    <div className={styles.settingGroup}>
      <div className={styles.sliderHeader}>
        <label className={styles.settingLabel} htmlFor={`${id}-chunk-size`}>Chunk Size (characters)</label>
        <span className={`${styles.sliderValue} mono`}>{local.chunkSize}</span>
      </div>
      <input id={`${id}-chunk-size`} type="range" min="100" max="8000" step="100" value={local.chunkSize} onChange={e => { const size = parseInt(e.target.value); setLocal({...local, chunkSize: size, chunkOverlap: Math.min(local.chunkOverlap, size - 1)}); }} />
    </div>
    <div className={styles.settingGroup}>
      <div className={styles.sliderHeader}>
        <label className={styles.settingLabel} htmlFor={`${id}-chunk-overlap`}>Chunk Overlap (characters)</label>
        <span className={`${styles.sliderValue} mono`}>{local.chunkOverlap}</span>
      </div>
      <input id={`${id}-chunk-overlap`} type="range" min="0" max={Math.min(2000, local.chunkSize - 1)} step="1" value={local.chunkOverlap} onChange={e => setLocal({...local, chunkOverlap: parseInt(e.target.value)})} />
      <p className={styles.settingHint}>New uploads use these settings. Rebuild the index in Advanced to update existing documents.</p>
    </div>
  </div>
  );
};
