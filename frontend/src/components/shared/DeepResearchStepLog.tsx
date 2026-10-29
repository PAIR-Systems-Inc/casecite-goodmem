import { Icon } from './Icon';
import type { DeepResearchStep } from '../../api/types';

interface DeepResearchStepLogProps {
  steps: DeepResearchStep[];
  styles: Record<string, string>;
}

const TOOL_LABEL: Record<string, string> = { documents: 'firm documents', case_law: 'case law' };

/** What each sub-question did: searches, passages, claims and their verification. */
export function DeepResearchStepLog({ steps, styles }: DeepResearchStepLogProps) {
  return (
    <ol className={styles.stepLog} aria-label="Step log">
      {steps.map((step, index) => (
        <li key={step.id} className={styles.step}>
          <div className={styles.stepHead}>
            <span className={styles.planIndex}>{index + 1}</span>
            <span className={styles.stepQuestion}>{step.question}</span>
            <span className={step.status === 'failed' ? styles.badgeWarn : styles.badgeMuted}>
              {step.status === 'failed' ? 'failed' : `${(step.duration_ms / 1000).toFixed(1)}s`}
            </span>
          </div>
          <ul className={styles.stepCalls}>
            {step.tool_calls.map((call, i) => (
              <li key={i} className={styles.stepCall}>
                <Icon name={call.error ? 'AlertTriangle' : 'Search'} size={11} />
                <span>
                  Searched {TOOL_LABEL[call.tool] || call.tool} for “{call.query}”:{' '}
                  {call.error ? <span className={styles.errorText}>{call.error}</span> : `${call.passages} passage${call.passages === 1 ? '' : 's'}`}
                </span>
              </li>
            ))}
            {step.followup_query && (
              <li className={styles.stepCall}>
                <Icon name="RotateCcw" size={11} />
                <span>Follow-up: “{step.followup_query}”</span>
              </li>
            )}
          </ul>
          {step.error && <div className={styles.errorText}>{step.error}</div>}
          {step.answer && <div className={styles.stepAnswer}>{step.answer}</div>}
          {step.claims.length > 0 && (
            <ul className={styles.claims}>
              {step.claims.map((claim, i) => (
                <li key={i} className={styles.claim}>
                  <Icon
                    name={claim.verified ? 'CheckCircle' : 'AlertCircle'}
                    size={12}
                    className={claim.verified ? styles.okIcon : styles.warnIcon}
                  />
                  <span>
                    {claim.text} <span className={styles.refs}>[{claim.refs.join(', ')}]</span>
                    {claim.quote && (
                      <span className={styles.claimQuote}>
                        {' '}“{claim.quote}” {claim.verified ? '' : '(quote not found in the cited passage)'}
                      </span>
                    )}
                  </span>
                </li>
              ))}
            </ul>
          )}
          <div className={styles.stepFoot}>
            {step.passages_found} passage{step.passages_found === 1 ? '' : 's'} found · {step.quotes_verified} verified · {step.quotes_unverified} unverified
            {step.invalid_refs_stripped ? ` · ${step.invalid_refs_stripped} invalid reference${step.invalid_refs_stripped === 1 ? '' : 's'} removed` : ''}
          </div>
        </li>
      ))}
    </ol>
  );
}
