import { Icon } from './Icon';
import { useCaseViewStore } from '../../stores/caseViewStore';
import { safeHttpUrl } from './safeUrl';
import type { DeepResearchSource, DeepResearchTreatment } from '../../api/types';

interface DeepResearchSourcesProps {
  sources: DeepResearchSource[];
  styles: Record<string, string>;
}

/** The citation check's outcome in plain words — never a verdict the scan cannot give. */
export function treatmentLabel(t: DeepResearchTreatment | undefined): { text: string; tone: 'ok' | 'warn' | 'muted' } | null {
  if (!t) return null;
  if (t.status === 'unavailable') return { text: 'Treatment not checked (no CourtListener token)', tone: 'muted' };
  if (t.status !== 'checked') return { text: 'Treatment check failed', tone: 'muted' };
  if (t.warning_level && t.warning_level !== 'none') {
    const n = (t.negative || 0) + (t.caution || 0);
    return { text: `Treatment language found in ${n} citing opinion${n === 1 ? '' : 's'} — read before relying`, tone: 'warn' };
  }
  if (t.is_good_law === true) return { text: 'No negative treatment language found', tone: 'ok' };
  return { text: 'Treatment undetermined', tone: 'muted' };
}

/** Every passage the answer cites: firm documents and court opinions, with the verified-quote count. */
export function DeepResearchSources({ sources, styles }: DeepResearchSourcesProps) {
  const { loadCaseDetail } = useCaseViewStore();
  if (sources.length === 0) {
    return <div className={styles.block}><div className={styles.muted}>The answer cites no passages.</div></div>;
  }
  return (
    <div className={styles.block}>
      <div className={styles.blockTitle}>Sources ({sources.length})</div>
      <ul className={styles.sources}>
        {sources.map((src) => {
          const treatment = treatmentLabel(src.treatment);
          const url = safeHttpUrl(src.url);
          return (
            <li key={src.ref} className={styles.source}>
              <span className={styles.sourceRef}>{src.ref}</span>
              <div className={styles.sourceBody}>
                <div className={styles.sourceTitle}>
                  <Icon name={src.type === 'case_law' ? 'Scale' : 'FileText'} size={13} />
                  <span>{src.title}</span>
                  {src.citation && <span className={styles.muted}>{src.citation}</span>}
                  {src.quotes_verified > 0 && (
                    <span className={styles.badgeOk} title="Quotes from this passage were located in its text">
                      <Icon name="CheckCircle" size={11} /> {src.quotes_verified} verified
                    </span>
                  )}
                  {src.type === 'case_law' && src.excerpt_source === 'snippet' && (
                    <span className={styles.badgeMuted} title="The opinion text could not be fetched; only the search snippet was read">
                      snippet only
                    </span>
                  )}
                </div>
                <blockquote className={styles.sourceQuote}>{src.snippet}</blockquote>
                <div className={styles.sourceMeta}>
                  {src.type === 'case_law' && src.opinion_id && (
                    <button type="button" className={styles.linkBtn} onClick={() => void loadCaseDetail(src.opinion_id!)}>
                      <Icon name="BookOpen" size={12} /> Open case
                    </button>
                  )}
                  {url && (
                    <a className={styles.linkBtn} href={url} target="_blank" rel="noopener noreferrer">
                      <Icon name="ExternalLink" size={12} /> CourtListener
                    </a>
                  )}
                  {treatment && (
                    <span className={treatment.tone === 'warn' ? styles.badgeWarn : treatment.tone === 'ok' ? styles.badgeOk : styles.badgeMuted}>
                      {treatment.tone === 'warn' && <Icon name="AlertTriangle" size={11} />}
                      {treatment.text}
                    </span>
                  )}
                </div>
              </div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
