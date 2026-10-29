import React from 'react';
import { Icon } from './Icon';
import { activateOnKey } from './activateOnKey';
import { safeHttpUrl } from './safeUrl';
import type { OpinionSummary, OpinionListResponse } from '../../types';
import type { JudgeOpinionSort } from '../../stores/judgeIntelStore';

interface JudgeOpinionsTabProps {
  styles: Record<string, string>;
  judgeId: string;
  selectedOpinion: OpinionSummary | null;
  setSelectedOpinion: (opinion: OpinionSummary | null) => void;
  loadOpinionFullText: (judgeId: string, opinionId: string) => void;
  judgeIntelOpinionQuery: string;
  setJudgeIntelOpinionQuery: (value: string) => void;
  judgeIntelOpinionSort: JudgeOpinionSort;
  queryJudgeOpinions: (judgeId: string, query?: string, offset?: number, sort?: JudgeOpinionSort) => void;
  judgeIntelLoading: boolean;
  judgeIntelOpinions: OpinionListResponse | null;
}

function JudgeOpinionsTab({
  styles,
  judgeId,
  selectedOpinion,
  setSelectedOpinion,
  loadOpinionFullText,
  judgeIntelOpinionQuery,
  setJudgeIntelOpinionQuery,
  judgeIntelOpinionSort,
  queryJudgeOpinions,
  judgeIntelLoading,
  judgeIntelOpinions,
}: JudgeOpinionsTabProps) {
  return (
    <div className={styles.judgeProfileSection}>
      <h3 className={styles.judgeProfileSectionTitle}>Opinions</h3>
      {selectedOpinion ? (
        <div className={styles.opinionFullView}>
          <button className={styles.backToResultsBtn} onClick={() => setSelectedOpinion(null)}>
            <Icon name="ChevronLeft" size={14} /> Back to opinions
          </button>
          <h3 className={styles.opinionFullTitle}>{selectedOpinion.case_name}</h3>
          <div className={styles.opinionFullMeta}>
            {selectedOpinion.court && <span>{selectedOpinion.court}</span>}
            {selectedOpinion.date_filed && <span>{selectedOpinion.date_filed}</span>}
            {selectedOpinion.citation && <span className="mono">{selectedOpinion.citation}</span>}
            {selectedOpinion.docket_number ? <span>No. {String(selectedOpinion.docket_number)}</span> : null}
            {(selectedOpinion.citation_count ?? 0) > 0 && <span>{selectedOpinion.citation_count} citations</span>}
            {safeHttpUrl(selectedOpinion.url) && (
              <a href={safeHttpUrl(selectedOpinion.url) ?? undefined} target="_blank" rel="noopener noreferrer" className={styles.wikiLink}>
                <Icon name="ExternalLink" size={11} className={styles.wikiLinkIcon} />CourtListener
              </a>
            )}
          </div>
          {selectedOpinion.full_text ? (
            <div className={styles.opinionFullText}>{selectedOpinion.full_text}</div>
          ) : (
            <div className={styles.opinionFullText}>
              CourtListener has no text for this opinion in its database
              {selectedOpinion.url ? ' — the scanned original may be available on the CourtListener page linked above.' : '.'}
            </div>
          )}
        </div>
      ) : (
        <>
          <div className={styles.opinionSearchBox}>
            <input
              type="text"
              className={styles.opinionSearchInput}
              placeholder="Search opinions (case name, text)..."
              aria-label="Search opinions"
              value={judgeIntelOpinionQuery}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => setJudgeIntelOpinionQuery(e.target.value)}
              onKeyDown={(e: React.KeyboardEvent<HTMLInputElement>) => e.key === 'Enter' && queryJudgeOpinions(judgeId, judgeIntelOpinionQuery)}
            />
            <button
              className={styles.opinionSearchBtn}
              onClick={() => queryJudgeOpinions(judgeId, judgeIntelOpinionQuery)}
              aria-label="Search opinions"
            >
              <Icon name="Search" size={14} />
            </button>
          </div>

          <div className={styles.scopeRow}>
            <span className={styles.scopeLabel}>Sort by</span>
            <button
              className={`${styles.scopePill} ${judgeIntelOpinionSort === 'citations' ? styles.scopePillActive : ''}`}
              onClick={() => queryJudgeOpinions(judgeId, judgeIntelOpinionQuery, 0, 'citations')}
            >
              Most Cited
            </button>
            <button
              className={`${styles.scopePill} ${judgeIntelOpinionSort === 'date' ? styles.scopePillActive : ''}`}
              onClick={() => queryJudgeOpinions(judgeId, judgeIntelOpinionQuery, 0, 'date')}
            >
              Newest
            </button>
          </div>

          {judgeIntelLoading && (
            <div className={styles.caseLoading}><Icon name="Loader2" size={20} className={styles.spinnerIcon} /> Loading opinions...</div>
          )}

          {judgeIntelOpinions && (
            <>
              <div className={styles.opinionResultsHeader}>{judgeIntelOpinions.total?.toLocaleString()} opinions</div>
              <div className={styles.opinionResultsList}>
                {judgeIntelOpinions.opinions?.map((op: OpinionSummary, i: number) => (
                  <div
                    key={`${op.id}-${i}`}
                    className={styles.opinionResultItem}
                    role="button"
                    tabIndex={0}
                    onClick={() => loadOpinionFullText(judgeId, op.id)}
                    onKeyDown={activateOnKey(() => loadOpinionFullText(judgeId, op.id))}
                  >
                    <div className={styles.judgeProfileItemRow}>
                      <div className={styles.opinionResultName}>{op.case_name}</div>
                      {safeHttpUrl(op.url) && (
                        <a
                          href={safeHttpUrl(op.url) ?? undefined}
                          target="_blank"
                          rel="noopener noreferrer"
                          className={styles.judgeProfileItemLink}
                          title="View on CourtListener"
                          onClick={(e) => e.stopPropagation()}
                        >
                          <Icon name="ExternalLink" size={12} />
                        </a>
                      )}
                    </div>
                    <div className={styles.opinionResultMeta}>
                      {op.citation && <span className="mono">{op.citation}</span>}
                      {op.court && <span>{op.court}</span>}
                      {op.date_filed && <span>{op.date_filed}</span>}
                      {op.docket_number ? <span>No. {String(op.docket_number)}</span> : null}
                      {(op.citation_count ?? 0) > 0 && <span>{op.citation_count} citations</span>}
                    </div>
                    {op.snippet && <div className={styles.opinionResultSnippet}>{op.snippet}...</div>}
                  </div>
                ))}
              </div>
              {(judgeIntelOpinions.opinions?.length ?? 0) < (judgeIntelOpinions.total ?? 0) && (
                <button
                  className={styles.loadMoreBtn}
                  onClick={() => queryJudgeOpinions(judgeId, judgeIntelOpinionQuery, judgeIntelOpinions.opinions?.length ?? 0)}
                  disabled={judgeIntelLoading}
                >
                  Load More ({judgeIntelOpinions.opinions?.length ?? 0} of {judgeIntelOpinions.total?.toLocaleString()})
                </button>
              )}
            </>
          )}
        </>
      )}
    </div>
  );
}

export { JudgeOpinionsTab };
export default JudgeOpinionsTab;
