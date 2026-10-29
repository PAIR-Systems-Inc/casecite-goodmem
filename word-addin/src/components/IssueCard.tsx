import type { ContractIssue } from '../api/types';
import s from '../styles/pane.module.css';

interface IssueCardProps {
  issue: ContractIssue;
  busy: boolean;
  onSelect: (issue: ContractIssue) => void;
  onApply: (issue: ContractIssue) => void;
}

function severityClass(severity: string): string {
  const level = severity.toLowerCase();
  if (level === 'critical' || level === 'major') return `${s.badge} ${s.badgeMajor}`;
  if (level === 'minor') return `${s.badge} ${s.badgeMinor}`;
  return s.badge;
}

/** One review issue: what, why, the quoted passage, and the two document actions. */
export function IssueCard({ issue, busy, onSelect, onApply }: IssueCardProps) {
  const canLocate = Boolean(issue.matched_text) && issue.grounding === 'span';
  const canApply = canLocate && Boolean(issue.suggested_language);
  return (
    <li className={s.issue}>
      <div className={s.issueHead}>
        <p className={s.issueTitle}>{issue.title}</p>
        <span className={severityClass(issue.severity)}>{issue.severity}</span>
      </div>
      {issue.why && <p className={s.why}>{issue.why}</p>}
      {issue.matched_text && <blockquote className={s.quote}>{issue.matched_text}</blockquote>}
      {issue.grounding === 'absence' && (
        <p className={s.muted}>Flagged because the contract does not address this; there is no passage to select.</p>
      )}
      {issue.grounding === 'unverified' && (
        <p className={s.muted}>The quoted passage could not be verified in the document text.</p>
      )}
      {issue.suggested_language && (
        <>
          <p className={s.label}>Suggested language</p>
          <blockquote className={s.quote}>{issue.suggested_language}</blockquote>
        </>
      )}
      <div className={s.row}>
        <button type="button" className={s.secondary} disabled={!canLocate || busy} onClick={() => onSelect(issue)}>
          {busy ? <span className={s.spinner} /> : null}
          Select in document
        </button>
        <button type="button" className={s.primary} disabled={!canApply || busy} onClick={() => onApply(issue)}>
          Insert as tracked change
        </button>
      </div>
    </li>
  );
}
