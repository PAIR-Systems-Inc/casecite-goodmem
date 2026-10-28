import { Icon } from './Icon';
import { MessageMarkdown } from './MessageMarkdown';
import { AiNotice } from './AiNotice';
import { DeepResearchSources } from './DeepResearchSources';
import { DeepResearchStepLog } from './DeepResearchStepLog';
import type { DeepResearchResult as DeepResearchResultData, DeepResearchRunDetail } from '../../api/types';

interface DeepResearchResultProps {
  result: DeepResearchResultData;
  run: DeepResearchRunDetail | null;
  showSteps: boolean;
  onToggleSteps: () => void;
  onClear: () => void;
  styles: Record<string, string>;
}

/** One honest line on how much of the answer is grounded, from the server's counters. */
export function verificationLine(result: DeepResearchResultData): string {
  const v = result.verification;
  const parts: string[] = [];
  parts.push(`${v.quotes_verified} quote${v.quotes_verified === 1 ? '' : 's'} verified against the source text`);
  if (v.quotes_unverified) parts.push(`${v.quotes_unverified} could not be located and ${v.quotes_unverified === 1 ? 'is' : 'are'} marked unverified`);
  if (v.invalid_refs_stripped) parts.push(`${v.invalid_refs_stripped} reference${v.invalid_refs_stripped === 1 ? '' : 's'} to no real source ${v.invalid_refs_stripped === 1 ? 'was' : 'were'} removed`);
  if (v.case_references_removed.length) parts.push(`${v.case_references_removed.length} unsupported case name${v.case_references_removed.length === 1 ? '' : 's'} redacted`);
  if (v.citations_checked) parts.push(`${v.citations_checked} cited authorit${v.citations_checked === 1 ? 'y' : 'ies'} checked for negative treatment`);
  return parts.join('; ') + '.';
}

/** The answer, its key findings and open questions, the sources it cites, and the step log. */
export function DeepResearchResult({ result, run, showSteps, onToggleSteps, onClear, styles }: DeepResearchResultProps) {
  const refLabel = (refs: string[]) => (refs.length ? ` [${refs.join(', ')}]` : '');
  return (
    <article className={styles.result} aria-label="Deep research answer">
      <div className={styles.resultHead}>
        <div className={styles.resultTitle}>
          <Icon name="FileText" size={14} />
          <span>Answer</span>
          {run?.created_at && <span className={styles.muted}>{new Date(run.created_at).toLocaleString()}</span>}
        </div>
        <button type="button" className={styles.iconBtn} onClick={onClear} aria-label="Clear answer" title="Clear">
          <Icon name="X" size={14} />
        </button>
      </div>

      <div className={styles.answer}>
        <MessageMarkdown text={result.answer || 'The run produced no answer text.'} />
      </div>

      {result.key_findings.length > 0 && (
        <div className={styles.block}>
          <div className={styles.blockTitle}>Key findings</div>
          <ul className={styles.findings}>
            {result.key_findings.map((f, i) => (
              <li key={i}>
                {f.text}
                <span className={styles.refs}>{refLabel(f.refs)}</span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {result.open_questions.length > 0 && (
        <div className={styles.block}>
          <div className={styles.blockTitle}>Not settled by this research</div>
          <ul className={styles.findings}>
            {result.open_questions.map((q, i) => <li key={i}>{q}</li>)}
          </ul>
        </div>
      )}

      <div className={styles.verification}>
        <Icon name="ShieldCheck" size={13} />
        <span>{verificationLine(result)}</span>
      </div>
      <div className={styles.coverage}>{result.scope.coverage_note}</div>

      <DeepResearchSources sources={result.sources} styles={styles} />

      <button type="button" className={styles.linkBtn} onClick={onToggleSteps} aria-expanded={showSteps}>
        <Icon name={showSteps ? 'ChevronUp' : 'ChevronDown'} size={13} />
        {showSteps ? 'Hide' : 'Show'} the step log ({result.sub_questions.length} step{result.sub_questions.length === 1 ? '' : 's'},{' '}
        {result.scope.tool_calls} search{result.scope.tool_calls === 1 ? '' : 'es'})
      </button>
      {showSteps && <DeepResearchStepLog steps={result.sub_questions} styles={styles} />}

      <AiNotice className={styles.notice} />
    </article>
  );
}
