import { Icon } from './Icon';
import type { CaseInfo } from '../../types';
import { safeHttpUrl } from './safeUrl';

interface CaseOpinion {
  id?: number;
  type?: string;
  type_label?: string;
  author?: string | null;
  per_curiam?: boolean;
  text?: string;
}

interface CaseDetailViewProps {
  styles: Record<string, string>;
  selectedCase: CaseInfo;
  caseLoading: boolean;
  setSelectedCase: (c: CaseInfo | null) => void;
}

function CaseDetailView({ styles, selectedCase, caseLoading, setSelectedCase }: CaseDetailViewProps) {
  const opinions = (Array.isArray(selectedCase.opinions) ? selectedCase.opinions : []) as CaseOpinion[];
  const hasOpinions = opinions.some((o) => (o.text || '').trim());
  const docketNumber = selectedCase.docket_number ? String(selectedCase.docket_number) : null;
  const natureOfSuit = selectedCase.nature_of_suit ? String(selectedCase.nature_of_suit) : null;
  const status = selectedCase.precedential_status ? String(selectedCase.precedential_status) : null;
  const headnotes = selectedCase.headnotes ? String(selectedCase.headnotes) : null;
  const posture = selectedCase.posture ? String(selectedCase.posture) : null;
  const url = selectedCase.url ? String(selectedCase.url) : null;

  return (
    <div className={styles.caseDetailView}>
      <button className={styles.backToResultsBtn} onClick={() => setSelectedCase(null)}>
        <Icon name="ChevronLeft" size={14} /> Back to results
      </button>
      {caseLoading ? (
        <div className={styles.caseLoading}>
          <Icon name="Loader2" size={20} className={styles.spinner} />
          <span>Loading case...</span>
        </div>
      ) : selectedCase.error ? (
        <div className={styles.toolErrorBox}>
          <Icon name="AlertCircle" size={14} />
          <span>{selectedCase.error}</span>
        </div>
      ) : (
        <>
          <h3 className={styles.caseDetailTitle}>{selectedCase.case_name}</h3>
          <div className={styles.caseDetailMeta}>
            {selectedCase.court && <span><Icon name="Building" size={12} /> {selectedCase.court}</span>}
            {selectedCase.date_filed && <span><Icon name="Calendar" size={12} /> {selectedCase.date_filed}</span>}
            {selectedCase.date_argued ? <span><Icon name="Mic" size={12} /> Argued {String(selectedCase.date_argued)}</span> : null}
            {docketNumber && <span><Icon name="Hash" size={12} /> {docketNumber}</span>}
            {(selectedCase.times_cited ?? 0) > 0 && <span><Icon name="Quote" size={12} /> Cited {selectedCase.times_cited!.toLocaleString()} times</span>}
            {status && <span><Icon name="BookOpen" size={12} /> {status}</span>}
          </div>
          {(selectedCase.citations?.length ?? 0) > 0 && (
            <div className={styles.caseDetailCitations}>
              {selectedCase.citations!.map((cite: string | Record<string, unknown>, i: number) => (
                <span key={i} className={styles.caseDetailCite}>
                  {typeof cite === 'string' ? cite : `${cite.volume || ''} ${cite.reporter || ''} ${cite.page || ''}`.trim() || 'Citation'}
                </span>
              ))}
            </div>
          )}
          {selectedCase.judges && (
            <div className={styles.caseDetailJudges}>
              <strong>Judges:</strong> {selectedCase.judges}
            </div>
          )}
          {(natureOfSuit || posture) && (
            <div className={styles.caseDetailJudges}>
              {natureOfSuit && <><strong>Nature of suit:</strong> {natureOfSuit}</>}
              {natureOfSuit && posture ? ' · ' : ''}
              {posture && <><strong>Posture:</strong> {posture}</>}
            </div>
          )}

          {selectedCase.syllabus && (
            <div className={styles.caseDetailSection}>
              <div className={styles.caseDetailSectionTitle}>Syllabus</div>
              <div className={styles.caseDetailText}>{selectedCase.syllabus}</div>
            </div>
          )}
          {headnotes && (
            <div className={styles.caseDetailSection}>
              <div className={styles.caseDetailSectionTitle}>Headnotes</div>
              <div className={styles.caseDetailText}>{headnotes}</div>
            </div>
          )}
          {hasOpinions ? (
            opinions.filter((o) => (o.text || '').trim()).map((o, i) => (
              <div key={o.id ?? i} className={styles.caseDetailSection}>
                <div className={styles.caseDetailSectionTitle}>
                  {o.type_label || 'Opinion'}
                  {o.per_curiam ? ' · per curiam' : o.author ? ` · ${o.author}` : ''}
                </div>
                <div className={styles.caseOpinionText}>{o.text}</div>
              </div>
            ))
          ) : selectedCase.opinion_text ? (
            <div className={styles.caseDetailSection}>
              <div className={styles.caseDetailSectionTitle}>Opinion</div>
              <div className={styles.caseOpinionText}>{selectedCase.opinion_text}</div>
            </div>
          ) : (
            <div className={styles.caseDetailSection}>
              <div className={styles.caseDetailText}>
                CourtListener has no opinion text for this case{url ? ' — open it on CourtListener for the scanned original.' : '.'}
              </div>
            </div>
          )}

          {url && (
            <a href={safeHttpUrl(url) ?? undefined} target="_blank" rel="noopener noreferrer" className={styles.oralArgLink}>
              <Icon name="ExternalLink" size={12} /> View on CourtListener
            </a>
          )}
        </>
      )}
    </div>
  );
}

export { CaseDetailView };
