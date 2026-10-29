import { Icon } from './Icon';
import type { DeepResearchRunSummary } from '../../api/types';

interface DeepResearchHistoryProps {
  runs: DeepResearchRunSummary[];
  loading: boolean;
  activeId: string | null;
  onOpen: (runId: string) => void;
  onDelete: (runId: string) => void;
  styles: Record<string, string>;
}

const STATUS_LABEL: Record<string, string> = {
  running: 'running',
  completed: 'done',
  failed: 'failed',
  cancelled: 'cancelled',
};

/** Past runs for the active matter (or everything the user can read), newest first. */
export function DeepResearchHistory({ runs, loading, activeId, onOpen, onDelete, styles }: DeepResearchHistoryProps) {
  if (!loading && runs.length === 0) return null;
  return (
    <div className={styles.history}>
      <div className={styles.blockTitle}>
        <Icon name="History" size={13} /> Past runs
        {loading && <Icon name="Loader2" size={12} className={styles.spin} />}
      </div>
      <ul className={styles.historyList}>
        {runs.map((run) => (
          <li key={run.id} className={`${styles.historyItem} ${run.id === activeId ? styles.historyItemActive : ''}`}>
            <button type="button" className={styles.historyOpen} onClick={() => onOpen(run.id)} disabled={run.status === 'running'}>
              <span className={styles.historyQuestion}>{run.question}</span>
              <span className={styles.historyMeta}>
                {new Date(run.created_at).toLocaleDateString()} · {run.sub_questions} step{run.sub_questions === 1 ? '' : 's'}
                {run.sources_cited !== null ? ` · ${run.sources_cited} source${run.sources_cited === 1 ? '' : 's'}` : ''} ·{' '}
                {STATUS_LABEL[run.status] || run.status}
                {run.role === 'member' ? ' · shared with you' : ''}
              </span>
            </button>
            {run.role === 'owner' && (
              <button type="button" className={styles.iconBtn} onClick={() => onDelete(run.id)} aria-label="Delete run" title="Delete">
                <Icon name="Trash2" size={13} />
              </button>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}
