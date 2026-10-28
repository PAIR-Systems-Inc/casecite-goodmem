import { Icon } from './Icon';
import { AiNotice } from './AiNotice';
import { mappingToCitation } from '../../utils';
import type { AuthorityMapChatResult, AuthorityMapping, ChatAuthorityMapRecord, ChatAuthorityMapRun } from '../../api/types';
import type { Citation } from '../../types/research';
import s from './AuthorityMapCard.module.css';

interface AuthorityMapCardProps {
  result: AuthorityMapChatResult;
  onCitationSelect: (citation: Citation) => void;
}

/**
 * Chat-message card for an exhaustive authority map: every proposition found
 * in the scoped file(s) with its verified (or unverified) supporting case.
 * Clicking a row opens the standard citation verification popup — ids match
 * the citations pushed into the Sources panel (same sort, same prefix).
 */
function AuthorityMapCard({ result, onCitationSelect }: AuthorityMapCardProps) {
  const t = result.totals;
  return (
    <div className={s.card}>
      <div className={s.header}>
        <Icon name="ShieldCheck" size={15} className={s.headerIcon} />
        <span className={s.title}>Authority map</span>
        <span className={s.totals}>
          {t.verified} verified · {t.authorities} authorities · {t.propositions} propositions
          {t.files > 1 ? ` · ${t.files} files` : ''}
        </span>
      </div>
      {result.files.map((f, fi) => {
        const ordered = [...f.mappings].sort((a, b) => Number(b.verified) - Number(a.verified));
        return (
          <div key={f.run_id} className={s.fileBlock}>
            {result.files.length > 1 && (
              <div className={s.fileName}>
                <Icon name="FileText" size={13} />
                <span className={s.fileNameText}>{f.document_name || 'Document'}</span>
                <span className={s.fileCounts}>
                  {f.summary.verified ?? 0}/{f.summary.authorities ?? 0} verified
                </span>
              </div>
            )}
            {ordered.length === 0 && (
              <p className={s.empty}>No supporting authorities found for this file.</p>
            )}
            {ordered.map((m: AuthorityMapping, i: number) => (
              <button
                key={`${f.run_id}-${i}`}
                className={s.row}
                onClick={() => onCitationSelect(mappingToCitation(m, i, `am${fi}`))}
              >
                <span className={m.verified ? s.badgeVerified : s.badgeUnverified}>
                  <Icon name={m.verified ? 'ShieldCheck' : 'ShieldAlert'} size={11} />
                  {m.verified ? 'Verified' : 'Unverified'}
                </span>
                <span className={s.rowBody}>
                  <span className={s.proposition}>{m.proposition}</span>
                  <span className={s.caseName}>
                    {m.case_name || m.citation || 'Authority'}
                    {m.citation && m.case_name ? ` — ${m.citation}` : ''}
                  </span>
                </span>
                <Icon name="ChevronRight" size={13} className={s.chevron} />
              </button>
            ))}
          </div>
        );
      })}
      {(result.coverage_notes?.length ?? 0) > 0 && (
        <div className={s.coverage} role="note">
          <Icon name="AlertCircle" size={12} className={s.coverageIcon} />
          <div>
            Not every file was covered in full:
            <ul className={s.coverageList}>
              {result.coverage_notes?.map((note, i) => <li key={i}>{note}</li>)}
            </ul>
          </div>
        </div>
      )}
      <AiNotice />
    </div>
  );
}

function countsLine(t?: { propositions?: number; authorities?: number; verified?: number } | null): string {
  return `${t?.verified ?? 0} verified · ${t?.authorities ?? 0} authorities · ${t?.propositions ?? 0} propositions`;
}

interface AuthorityMapRecordCardProps {
  record: ChatAuthorityMapRecord;
  /** Open the stored run (GET /authority-map/{run_id}) in Case Citations. */
  onOpenRun: (runId: string, run: ChatAuthorityMapRun) => void;
}

/**
 * Compact card for a chat-triggered authority map restored from History: the
 * stored record keeps only the job status and one summary per file, so each
 * row opens its full map from the run id instead of listing every authority.
 */
function AuthorityMapRecordCard({ record, onOpenRun }: AuthorityMapRecordCardProps) {
  const status = record.status;
  const runs = record.runs ?? [];
  const statusText = status === 'completed'
    ? countsLine(record.totals)
    : status === 'running'
      ? 'Still running — reopen this conversation later for the result.'
      : `Failed${record.error ? ` (${record.error})` : ''}`;
  return (
    <div className={s.card}>
      <div className={s.header}>
        <Icon name={status === 'completed' ? 'ShieldCheck' : status === 'running' ? 'Loader2' : 'ShieldAlert'} size={15} className={s.headerIcon} />
        <span className={s.title}>Authority map</span>
        <span className={s.totals}>{statusText}</span>
      </div>
      {runs.map((run, i) => (
        <button
          key={run.run_id ?? `${run.document_id ?? 'doc'}-${i}`}
          className={s.row}
          disabled={!run.run_id}
          title={run.run_id ? 'Open this map in Case Citations' : 'No stored map for this file'}
          onClick={() => run.run_id && onOpenRun(run.run_id, run)}
        >
          <Icon name="FileText" size={13} className={s.chevron} />
          <span className={s.rowBody}>
            <span className={s.caseName}>{run.document_name || 'Document'}</span>
            <span className={s.proposition}>{countsLine(run.summary)}</span>
          </span>
          {run.run_id && <Icon name="ChevronRight" size={13} className={s.chevron} />}
        </button>
      ))}
      {runs.length === 0 && record.documents.length > 0 && (
        <p className={s.empty}>
          {record.documents.map((d) => d.name).join(', ')}
        </p>
      )}
      {(record.coverage_notes?.length ?? 0) > 0 && (
        <div className={s.coverage} role="note">
          <Icon name="AlertCircle" size={12} className={s.coverageIcon} />
          <div>
            Not every file was covered in full:
            <ul className={s.coverageList}>
              {record.coverage_notes?.map((note, i) => <li key={i}>{note}</li>)}
            </ul>
          </div>
        </div>
      )}
      <AiNotice />
    </div>
  );
}

export { AuthorityMapCard, AuthorityMapRecordCard };
