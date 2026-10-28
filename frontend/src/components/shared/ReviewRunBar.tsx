import { Icon } from './Icon';
import type { ReviewTableEstimate } from '../../api/types';
import type { RunProgress } from '../../stores/reviewTablesStore';

interface ReviewRunBarProps {
  estimate: ReviewTableEstimate | null;
  running: boolean;
  progress: RunProgress | null;
  runError: string | null;
  canRun: boolean;
  onRun: () => void;
  styles: Record<string, string>;
}

const formatTokens = (n: number) => (n >= 1000 ? `${Math.round(n / 1000)}k` : String(n));

/** The cost of a full run, up front, and the button that starts it. */
export function ReviewRunBar({ estimate, running, progress, runError, canRun, onRun, styles }: ReviewRunBarProps) {
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
        ) : estimate ? (
          <>
            <Icon name="Info" size={14} />
            <span>
              A full run extracts <strong>{estimate.cells}</strong> cell{estimate.cells === 1 ? '' : 's'} with{' '}
              <strong>{estimate.llm_calls}</strong> model call{estimate.llm_calls === 1 ? '' : 's'} (~{formatTokens(estimate.approx_input_tokens)} input tokens,{' '}
              {estimate.model}). Every value carries the quote that supports it; verify before relying on it.
            </span>
          </>
        ) : (
          <span className={styles.muted}>Add documents and fields, then run the review.</span>
        )}
        {runError && !running && <span className={styles.runError}>{runError}</span>}
      </div>
      <button type="button" className={styles.primaryBtn} onClick={onRun} disabled={!canRun || running}>
        <Icon name={running ? 'Loader2' : 'Play'} size={14} className={running ? styles.spin : undefined} />
        {running ? 'Running…' : 'Run review'}
      </button>
    </div>
  );
}
