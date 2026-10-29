import { IssueCard } from '../components/IssueCard';
import { KeyTermsTable } from '../components/KeyTermsTable';
import { Notice } from '../components/Notice';
import { useAddinStore } from '../store';
import s from '../styles/pane.module.css';

/** Analyze the open document and act on the findings in place. */
export function Analyze() {
  const { analysis, setRepresenting, analyze, selectIssue, applyRewrite, selectKeyTerm } = useAddinStore();
  const running = analysis.status === 'reading' || analysis.status === 'running';

  return (
    <>
      <div className={s.card}>
        <h2 className={s.heading}>Analyze this document</h2>
        <p className={s.text}>
          Reads the whole document and runs CaseCite's contract review on it. The text is sent to
          your CaseCite server and from there to the AI provider your firm configured.
        </p>
        <div className={s.field}>
          <label className={s.label} htmlFor="representing">
            Representing (optional)
          </label>
          <input
            id="representing"
            className={s.input}
            placeholder='e.g. "Customer" or "Landlord"'
            value={analysis.representing}
            onChange={(e) => setRepresenting(e.target.value)}
            disabled={running}
          />
        </div>
        <button type="button" className={s.primary} onClick={analyze} disabled={running}>
          {running ? <span className={s.spinner} /> : null}
          {analysis.status === 'reading'
            ? 'Reading document…'
            : analysis.status === 'running'
              ? 'Analyzing…'
              : analysis.result
                ? 'Analyze again'
                : 'Analyze'}
        </button>
        {analysis.status === 'error' && (
          <p className={s.error} role="alert">
            {analysis.error}
          </p>
        )}
      </div>

      {analysis.result && (
        <>
          <div className={s.card}>
            <h2 className={s.heading}>Summary</h2>
            <p className={s.muted}>Contract type: {analysis.result.contract_type}</p>
            {analysis.result.executive_summary && (
              <p className={s.summary}>{analysis.result.executive_summary}</p>
            )}
            <Notice />
          </div>

          <div className={s.card}>
            <h2 className={s.heading}>Key terms</h2>
            <KeyTermsTable terms={analysis.result.key_terms} onSelect={selectKeyTerm} />
          </div>

          <div className={s.card}>
            <h2 className={s.heading}>Issues ({analysis.result.issues.length})</h2>
            {analysis.note && <p className={s.note}>{analysis.note}</p>}
            {analysis.result.issues.length === 0 ? (
              <p className={s.muted}>No issues were raised.</p>
            ) : (
              <ul className={s.list}>
                {analysis.result.issues.map((issue) => (
                  <IssueCard
                    key={issue.ref}
                    issue={issue}
                    busy={analysis.busyRef === issue.ref}
                    onSelect={selectIssue}
                    onApply={applyRewrite}
                  />
                ))}
              </ul>
            )}
            <Notice />
          </div>
        </>
      )}
    </>
  );
}
