import { Icon } from './Icon';
import type { RunProgress } from '../../stores/workflowsStore';

interface WorkflowRunBarProps {
  stepCount: number;
  matterName: string | null;
  running: boolean;
  progress: RunProgress | null;
  runError: string | null;
  canRun: boolean;
  onPickDocuments: () => void;
  onRunOnMatter: () => void;
  onCancel: () => void;
  styles: Record<string, string>;
}

/** Start a run over picked documents or the whole matter; progress and cancel while it runs. */
export function WorkflowRunBar({
  stepCount, matterName, running, progress, runError, canRun, onPickDocuments, onRunOnMatter, onCancel, styles,
}: WorkflowRunBarProps) {
  return (
    <div className={styles.runBar}>
      <div className={styles.runInfo}>
        {running && progress ? (
          <>
            <Icon name="Loader2" size={14} className={styles.spin} />
            <span>{progress.message}</span>
            {progress.fraction !== null && (
              <div className={styles.progressTrack} aria-hidden="true">
                <div className={styles.progressFill} style={{ width: `${Math.round(progress.fraction * 100)}%` }} />
              </div>
            )}
          </>
        ) : (
          <>
            <Icon name="Info" size={14} />
            <span>
              A run executes <strong>{stepCount}</strong> step{stepCount === 1 ? '' : 's'} in order over the documents you pick
              {matterName ? <> or over everything filed under <strong>{matterName}</strong></> : null}. Each step&apos;s output is kept; a
              failed step can be resumed. Verify every output before relying on it.
            </span>
          </>
        )}
        {runError && !running && <span className={styles.runError}>{runError}</span>}
      </div>
      {running ? (
        <button type="button" className={styles.secondaryBtn} onClick={onCancel}>
          <Icon name="X" size={14} /> Cancel
        </button>
      ) : (
        <>
          {matterName && (
            <button type="button" className={styles.secondaryBtn} onClick={onRunOnMatter} disabled={!canRun}>
              <Icon name="Target" size={14} /> Run on matter
            </button>
          )}
          <button type="button" className={styles.primaryBtn} onClick={onPickDocuments} disabled={!canRun}>
            <Icon name="Play" size={14} /> Run on documents…
          </button>
        </>
      )}
    </div>
  );
}
