import { Icon } from './Icon';
import { AiNotice } from './AiNotice';
import type { ToolResultData, CitingCase } from './ToolResultsArea';
import { safeHttpUrl } from './safeUrl';

interface CitationValidationResultProps {
  styles: Record<string, string>;
  tr: ToolResultData;
}

const TREATMENT_LABEL: Record<string, string> = {
  negative: 'Negative language',
  caution: 'Cautionary language',
  positive: 'Positive language',
  neutral: 'Cited',
};

// What the scan found, stated as what it is: language in citing opinions,
// not an editorial judgment that the case is or is not good law.
const BADGE: Record<string, { icon: string; label: string; className: string }> = {
  none: { icon: 'CheckCircle', label: 'No negative language found', className: 'validationBadgeNone' },
  caution: { icon: 'AlertCircle', label: 'Cautionary language found', className: 'validationBadgeCaution' },
  warning: { icon: 'AlertTriangle', label: 'Negative language found — read before relying', className: 'validationBadgeWarning' },
  danger: { icon: 'AlertTriangle', label: 'Repeated negative language — likely bad law, verify', className: 'validationBadgeDanger' },
  unknown: { icon: 'HelpCircle', label: 'No treatment signal available', className: 'validationBadgeUnknown' },
};

function CitationValidationResult({ styles, tr }: CitationValidationResultProps) {
  const citing = (tr.citing_cases || []) as CitingCase[];
  const grouped = ['negative', 'caution', 'positive'].map((t) => ({
    treatment: t,
    cases: citing.filter((c) => c.treatment === t),
  })).filter((g) => g.cases.length > 0);
  const badge = BADGE[tr.warning_level || 'unknown'] || BADGE.unknown;
  const read = tr.citing_cases_analyzed;

  return (
    <div className={styles.resultCard}>
      <div className={styles.toolOverline}>Citation check · CourtListener</div>
      <div className={styles.validationBadgeWrapper}>
        <div className={styles.validationCaseName}>
          {tr.case_name || 'Unknown Case'}
        </div>
        <div className={styles.validationCitation}>{tr.citation}</div>
        {tr.absolute_url && (
          <a className={styles.validationCaseLink} href={safeHttpUrl(tr.absolute_url) ?? undefined} target="_blank" rel="noopener noreferrer">
            <Icon name="ExternalLink" size={12} /> Open case on CourtListener
          </a>
        )}
      </div>
      <div className={`${styles.validationBadge} ${styles[badge.className]}`}>
        <Icon name={badge.icon} size={16} />
        {badge.label}
      </div>
      <div className={styles.validationMethod}>
        Scan of the language courts use near this citation
        {typeof read === 'number' && read > 0 ? ` in ${read} recent and most-cited citing opinions` : ''}.
        Not an editorial citator — it does not replace Shepard&rsquo;s or KeyCite.
      </div>
      <div className={styles.validationGrid}>
        <div className={styles.validationItem}>
          <span className={styles.validationLabel}>Total Citations</span>
          <span className={styles.validationValue}>{(tr.total_citing_cases || 0).toLocaleString()}</span>
        </div>
        <div className={styles.validationItem}>
          <span className={styles.validationLabel}>Positive</span>
          <span className={styles.validationValuePositive}>{tr.positive_citations || 0}</span>
        </div>
        <div className={styles.validationItem}>
          <span className={styles.validationLabel}>Caution</span>
          <span className={styles.validationValue}>{tr.caution_citations || 0}</span>
        </div>
        <div className={styles.validationItem}>
          <span className={styles.validationLabel}>Negative</span>
          <span className={styles.validationValueNegative}>{tr.negative_citations || 0}</span>
        </div>
        {tr.last_cited && (
          <div className={styles.validationItem}>
            <span className={styles.validationLabel}>Last Cited</span>
            <span className={styles.validationValue}>{tr.last_cited}</span>
          </div>
        )}
      </div>

      {grouped.map((g) => (
        <div key={g.treatment} className={styles.caseDetailSection}>
          <div className={styles.caseDetailSectionTitle}>
            {TREATMENT_LABEL[g.treatment]} ({g.cases.length})
          </div>
          {g.cases.map((c, i) => (
            <div key={`${c.id}-${i}`} className={styles.resultRow}>
              <div className={styles.resultRowContent}>
                <span className={styles.resultRowTitle}>{c.case_name}</span>
                <span className={styles.resultRowMeta}>
                  {[c.citation, c.court, c.date_filed].filter(Boolean).join(' • ')}
                </span>
                {c.snippet && (
                  <span className={styles.resultSnippet}>
                    {c.context_found ? '' : 'Opening lines: '}“{c.snippet}”
                  </span>
                )}
              </div>
              {c.url && (
                <a href={safeHttpUrl(c.url) ?? undefined} target="_blank" rel="noopener noreferrer" title="View on CourtListener">
                  <Icon name="ExternalLink" size={14} className={styles.resultRowLinkIcon} />
                </a>
              )}
            </div>
          ))}
        </div>
      ))}

      {tr.notes && tr.notes.length > 0 && (
        <div className={styles.analysisNotes}>
          <div className={styles.analysisNotesTitle}>How this was checked</div>
          {tr.notes.map((note, i) => (
            <div key={i} className={styles.analysisNote}>• {note}</div>
          ))}
        </div>
      )}
      <AiNotice />
    </div>
  );
}

export { CitationValidationResult };
