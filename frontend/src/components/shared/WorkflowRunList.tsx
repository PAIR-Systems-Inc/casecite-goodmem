import { Icon } from './Icon';
import { activateOnKey } from './activateOnKey';
import type { WorkflowRunSummary } from '../../api/types';

interface WorkflowRunListProps {
  runs: WorkflowRunSummary[];
  selectedRunId: string | null;
  running: boolean;
  onSelect: (runId: string) => void;
  onResume: (runId: string) => void;
  styles: Record<string, string>;
}

const STATUS_ICON: Record<string, string> = {
  running: 'Loader2',
  completed: 'CheckCircle',
  failed: 'XCircle',
  cancelled: 'Minus',
};

/** Run history: one row per run with its status and step count; failed runs offer Resume. */
export function WorkflowRunList({ runs, selectedRunId, running, onSelect, onResume, styles }: WorkflowRunListProps) {
  if (runs.length === 0) {
    return <div className={styles.runEmpty}>No runs yet.</div>;
  }
  return (
    <ul className={styles.runList}>
      {runs.map((run) => {
        const resumable = (run.status === 'failed' || run.status === 'cancelled') && !running;
        return (
          <li
            key={run.id}
            className={`${styles.runRow} ${run.id === selectedRunId ? styles.runRowActive : ''}`}
            role="button"
            tabIndex={0}
            onClick={() => onSelect(run.id)}
            onKeyDown={activateOnKey(() => onSelect(run.id))}
          >
            <Icon
              name={STATUS_ICON[run.status] || 'Clock'}
              size={14}
              className={`${styles[`status_${run.status}`] || ''} ${run.status === 'running' ? styles.spin : ''}`}
            />
            <div className={styles.runRowBody}>
              <div className={styles.runRowTitle}>
                {new Date(run.created_at).toLocaleString()}
                <span className={styles.runRowStatus}>{run.status}</span>
              </div>
              <div className={styles.runRowMeta}>
                {run.completed_steps}/{run.steps} steps · {run.documents} document{run.documents === 1 ? '' : 's'}
                {run.resumed_count > 0 ? ` · resumed ${run.resumed_count}×` : ''}
              </div>
            </div>
            {resumable && (
              <button
                type="button"
                className={styles.secondaryBtn}
                onClick={(e) => { e.stopPropagation(); onResume(run.id); }}
                title="Re-run from the failed step with earlier outputs kept"
              >
                <Icon name="RotateCcw" size={13} /> Resume
              </button>
            )}
          </li>
        );
      })}
    </ul>
  );
}
