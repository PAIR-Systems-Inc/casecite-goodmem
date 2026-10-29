import { Icon } from './Icon';
import { activateOnKey } from './activateOnKey';
import { safeHttpUrl } from './safeUrl';
import type { ToolResultData } from './ToolResultsArea';

interface CaseSearchResultsProps {
  styles: Record<string, string>;
  tr: ToolResultData;
  toolLoading: boolean;
  loadCaseDetail: (id: string, kind?: 'cluster' | 'opinion') => void;
  loadMoreResults: () => void;
}

function CaseSearchResults({ styles, tr, toolLoading, loadCaseDetail, loadMoreResults }: CaseSearchResultsProps) {
  return (
    <div className={`${styles.toolCard} ${styles.caseSearchResults}`}>
      <div className={styles.toolOverline}>Case Lookup · CourtListener</div>
      <div className={styles.searchResultsHeader}>
        <span>{tr.count?.toLocaleString() || 0} cases found</span>
        <span className={styles.searchResultsPage}>Showing {tr.results?.length || 0}</span>
      </div>
      <div className={styles.caseResultsList}>
        {tr.results!.map((c, i) => {
          const citations = Array.isArray(c.citation) ? (c.citation as string[]) : c.citation ? [String(c.citation)] : [];
          const status = typeof c.status === 'string' ? c.status : null;
          const url = safeHttpUrl(c.url);
          return (
            <div
              key={`${c.id}-${i}`}
              className={`case-result ${styles.caseResultItem}`}
              role="button"
              tabIndex={0}
              onClick={() => loadCaseDetail(c.id, 'cluster')}
              onKeyDown={activateOnKey(() => loadCaseDetail(c.id, 'cluster'))}
              style={{ cursor: 'pointer' }}
            >
              <div className={styles.caseResultContent}>
                <div className={styles.caseResultName}>{c.case_name}</div>
                <div className={styles.caseResultMeta}>
                  {c.court && <span>{c.court}</span>}
                  {c.date_filed && <span>{c.date_filed}</span>}
                  {c.docket_number ? <span>No. {String(c.docket_number)}</span> : null}
                  {(c.times_cited ?? 0) > 0 && <span>{c.times_cited!.toLocaleString()} citations</span>}
                  {status && status !== 'Published' && <span>{status}</span>}
                </div>
                {citations.length > 0 && (
                  <div className={styles.caseResultCitation}>{citations.join(' · ')}</div>
                )}
                {c.judges ? <div className={styles.caseResultMeta}><span>{c.judges}</span></div> : null}
                {c.snippet ? <div className={styles.resultSnippet}>{String(c.snippet)}</div> : null}
              </div>
              {url ? (
                <a
                  href={url}
                  target="_blank"
                  rel="noopener noreferrer"
                  onClick={(e) => e.stopPropagation()}
                  title="View on CourtListener"
                  className={styles.caseResultLink}
                >
                  <Icon name="ExternalLink" size={14} className={styles.resultRowLinkIcon} />
                </a>
              ) : null}
            </div>
          );
        })}
      </div>
      {tr.has_next && (
        <button
          className={styles.loadMoreBtn}
          onClick={loadMoreResults}
          disabled={toolLoading}
        >
          {toolLoading ? 'Loading...' : `Load More (${tr.results!.length} of ${tr.count?.toLocaleString()})`}
        </button>
      )}
    </div>
  );
}

export { CaseSearchResults };
