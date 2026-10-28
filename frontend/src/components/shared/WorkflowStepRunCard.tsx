import { useState } from 'react';
import { Icon } from './Icon';
import { stepKindInfo } from '../../stores/workflowsStore';
import { appNavigate } from '../../utils/router';
import type { WorkflowStepRun } from '../../api/types';

interface WorkflowStepRunCardProps {
  step: WorkflowStepRun;
  styles: Record<string, string>;
}

const STATUS_ICON: Record<string, string> = {
  pending: 'Clock',
  running: 'Loader2',
  completed: 'CheckCircle',
  failed: 'XCircle',
  skipped: 'Minus',
};

const text = (v: unknown) => (typeof v === 'string' ? v : '');

/** One step of a run: status, the one-line summary, the error, and the output behind a toggle. */
export function WorkflowStepRunCard({ step, styles }: WorkflowStepRunCardProps) {
  const [open, setOpen] = useState(false);
  const info = stepKindInfo(step.kind);
  const data = (step.output?.data || {}) as Record<string, unknown>;
  const tables = step.produced_ids?.review_tables || [];
  const research = step.produced_ids?.research_runs || [];
  const analyses = step.produced_ids?.analyses || [];
  const draft = step.kind === 'draft_document' ? text(data.text) : '';
  const answer = step.kind === 'deep_research' ? text(data.answer) : '';
  const hasDetail = !!(draft || answer || tables.length || analyses.length || research.length || Object.keys(data).length);

  return (
    <div className={`${styles.stepRun} ${styles[`stepRun_${step.status}`] || ''}`}>
      <div className={styles.stepRunHead}>
        <Icon
          name={STATUS_ICON[step.status] || 'Clock'}
          size={15}
          className={`${styles[`status_${step.status}`] || ''} ${step.status === 'running' ? styles.spin : ''}`}
        />
        <span className={styles.stepIndex}>{step.index + 1}</span>
        <span className={styles.stepRunName}>{step.name}</span>
        <span className={styles.stepKind}><Icon name={info.icon} size={12} /> {info.title}</span>
        <span className={styles.stepRunStatus}>{step.status}</span>
        {step.finished_at && step.started_at && (
          <span className={styles.muted}>
            {Math.max(1, Math.round((new Date(step.finished_at).getTime() - new Date(step.started_at).getTime()) / 1000))}s
          </span>
        )}
        {hasDetail && (
          <button type="button" className={styles.iconBtn} onClick={() => setOpen((o) => !o)} aria-label={open ? 'Hide output' : 'Show output'} title={open ? 'Hide output' : 'Show output'}>
            <Icon name={open ? 'ChevronUp' : 'ChevronDown'} size={14} />
          </button>
        )}
      </div>
      {step.output?.summary && <div className={styles.stepRunSummary}>{step.output.summary}</div>}
      {step.error && <div className={styles.stepRunError}>{step.error}</div>}
      {open && (
        <div className={styles.stepRunDetail}>
          {tables.length > 0 && (
            <div className={styles.linkRow}>
              {tables.map((id) => (
                <button key={id} type="button" className={styles.linkBtn} onClick={() => appNavigate(`/review-tables/${encodeURIComponent(id)}`)}>
                  <Icon name="Table2" size={13} /> Open review table
                </button>
              ))}
            </div>
          )}
          {analyses.length > 0 && (
            <div className={styles.linkRow}>
              <span className={styles.muted}>{analyses.length} contract analysis{analyses.length === 1 ? '' : 'es'} saved under Contracts.</span>
            </div>
          )}
          {research.length > 0 && (
            <div className={styles.linkRow}>
              <span className={styles.muted}>Research run saved in the Research view&apos;s deep research history.</span>
            </div>
          )}
          {draft && (
            <>
              {text(data.title) && <div className={styles.stepRunTitle}>{text(data.title)}</div>}
              <pre className={styles.outputText}>{draft}</pre>
            </>
          )}
          {answer && <pre className={styles.outputText}>{answer}</pre>}
          {!draft && !answer && (
            <pre className={styles.outputJson}>{JSON.stringify(data, null, 2)}</pre>
          )}
        </div>
      )}
    </div>
  );
}
