import { Icon } from './Icon';
import { CaseSearchResults } from './CaseSearchResults';
import { CaseDetailView } from './CaseDetailView';
import { CitationValidationResult } from './CitationValidationResult';
import { DocketDetailView } from './DocketDetailView';
import type { DocketDetail } from './DocketDetailView';
import { TrendChart } from './TrendChart';
import type { CaseInfo, JudgeSearchResult } from '../../types';
import { safeHttpUrl } from './safeUrl';

export interface TrendYear { year: number; count: number }
interface PrecedentResult {
  id?: number;
  opinion_id?: number | null;
  case_name: string;
  citation: string;
  citations?: string[];
  citation_count?: number;
  times_cited?: number;
  court?: string;
  court_id?: string | null;
  date_filed?: string | null;
  docket_number?: string | null;
  judges?: string | null;
  status?: string | null;
  url?: string;
  snippet?: string | null;
}
interface DocketResult {
  id?: number;
  case_name: string;
  case_name_full?: string | null;
  court: string;
  court_id?: string | null;
  docket_number: string;
  date_filed?: string | null;
  date_terminated?: string | null;
  date_argued?: string | null;
  nature_of_suit?: string | null;
  cause?: string | null;
  jury_demand?: string | null;
  jurisdiction_type?: string | null;
  assigned_to?: string | null;
  referred_to?: string | null;
  parties?: { name: string }[];
  attorneys?: { name: string }[];
  url?: string;
}
interface OralArgResult {
  id?: number;
  case_name: string;
  case_name_full?: string | null;
  court: string;
  court_id?: string | null;
  date_argued: string;
  docket_number?: string | null;
  judges?: string | null;
  duration?: number | null;
  duration_label?: string | null;
  audio_url?: string | null;
  download_url?: string | null;
  snippet?: string | null;
  url?: string;
}
export interface CitingCase {
  id?: number;
  case_name?: string;
  citation?: string | null;
  court?: string | null;
  date_filed?: string | null;
  treatment?: 'negative' | 'caution' | 'positive' | 'neutral' | string;
  snippet?: string | null;
  context_found?: boolean;
  url?: string | null;
}

export interface ToolResultData {
  error?: string;
  results?: CaseInfo[];
  count?: number;
  has_next?: boolean;
  case_name?: string;
  citation?: string;
  warning_level?: string;
  total_citing_cases?: number;
  citing_cases_analyzed?: number;
  positive_citations?: number;
  caution_citations?: number;
  negative_citations?: number;
  neutral_count?: number;
  citing_cases?: CitingCase[];
  analysis_basis?: string;
  last_cited?: string;
  absolute_url?: string;
  notes?: string[];
  judges?: JudgeSearchResult[];
  precedents?: PrecedentResult[];
  dockets?: DocketResult[];
  arguments?: OralArgResult[];
  years?: TrendYear[];
  years_failed?: number;
  total_cases?: number;
  trend?: string;
  trend_basis?: { from_year: number; to_year: number };
  current_year_partial?: boolean;
}

interface ActiveTool {
  id: string;
  name?: string;
  placeholder?: string;
  [key: string]: unknown;
}

interface ToolResultsAreaProps {
  styles: Record<string, string>;
  tr: ToolResultData | null;
  activeTool: ActiveTool | null;
  toolInput: string;
  toolLoading: boolean;
  toolApiSlow: boolean;
  selectedCase: CaseInfo | null;
  selectedJudge: string | null;
  caseLoading: boolean;
  selectedDocket: DocketDetail | null;
  docketLoading: boolean;
  loadDocketDetail: (id: number) => void;
  setSelectedDocket: (d: DocketDetail | null) => void;
  loadPrecedentCase: (id: number | string) => void;
  loadCaseDetail: (id: string, kind?: 'cluster' | 'opinion') => void;
  setSelectedCase: (c: CaseInfo | null) => void;
  setSelectedJudge: (j: string | null) => void;
  loadMoreResults: () => void;
  setActiveMode: (mode: string) => void;
  setActiveTool: (tool: ActiveTool | null) => void;
  setToolResult: (v: Record<string, unknown> | null) => void;
  buildJudgeIntel: (id: string, name: string) => void;
}

const joinMeta = (parts: Array<string | null | undefined>) => parts.filter(Boolean).join(' • ');

function ToolResultsArea({
  styles, tr, activeTool, toolInput, toolLoading, toolApiSlow,
  selectedCase, selectedJudge, caseLoading,
  selectedDocket, docketLoading, loadDocketDetail, setSelectedDocket,
  loadPrecedentCase,
  loadCaseDetail, setSelectedCase, setSelectedJudge: _setSelectedJudge,
  loadMoreResults, setActiveMode, setActiveTool, setToolResult, buildJudgeIntel,
}: ToolResultsAreaProps) {
  return (
    <div className={styles.trsArea}>

      {tr?.error && (
        <div className={styles.toolErrorBox}>
          <Icon name="AlertCircle" size={14} />
          <span>{tr.error}</span>
        </div>
      )}

      {/* CourtListener API Slow Warning - shows during loading after 10s */}
      {(toolApiSlow && toolLoading) && (
        <div className={styles.apiSlowWarning}>
          <Icon name="Clock" size={16} className={styles.apiSlowIcon} />
          <div>
            <strong className={styles.apiSlowTitle}>CourtListener API is Taking a While</strong>
            <span>The third-party legal database is taking a while to respond. Please be patient while we wait for their servers.</span>
          </div>
        </div>
      )}

      {/* Case Search Results */}
      {activeTool?.id === 'case-lookup' && tr?.results && !selectedCase && (
        <CaseSearchResults styles={styles} tr={tr} toolLoading={toolLoading} loadCaseDetail={loadCaseDetail} loadMoreResults={loadMoreResults} />
      )}

      {/* Case Detail View */}
      {activeTool?.id === 'case-lookup' && selectedCase && (
        <CaseDetailView styles={styles} selectedCase={selectedCase} caseLoading={caseLoading} setSelectedCase={setSelectedCase} />
      )}

      {/* Citation Validation Result */}
      {activeTool?.id === 'validate-citation' && tr && !tr.error && (
        <CitationValidationResult styles={styles} tr={tr} />
      )}

      {/* Judge Search Results - Redirect to full Judge Intel view */}
      {activeTool?.id === 'judge-analyzer' && tr?.judges && !selectedJudge && (
        <div className={`${styles.toolCard} ${styles.caseSearchResults}`}>
          <div className={styles.toolOverline}>Judge Intel · CourtListener</div>
          <div className={styles.toolTitle}>"{toolInput}"</div>
          <div className={styles.searchResultsHeader}>
            <span>{tr.count || tr.judges.length} judges found</span>
          </div>
          <div className={styles.caseResultsList}>
            {tr.judges.map((j, i) => (
              <div
                key={`${j.id}-${i}`}
                className={`case-result ${styles.caseResultItem}`}
                onClick={() => {
                  // Switch to full Judge Intel mode in main area
                  setActiveMode('judge-intel');
                  setActiveTool(null);
                  setToolResult(null);
                  buildJudgeIntel(j.id, j.name || j.name_full || '');
                }}
              >
                <div className={styles.caseResultName}>{j.name || j.name_full}</div>
                <div className={styles.caseResultMeta}>
                  {j.court && <span>{j.court}</span>}
                  {j.position && <span>{j.position}</span>}
                  {j.appointed_by && <span>Appointed by {j.appointed_by}</span>}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}


      {/* Precedent opinion — one level deep, Back to results returns to the list */}
      {activeTool?.id === 'precedents' && (selectedCase || caseLoading) && (
        <CaseDetailView
          styles={styles}
          selectedCase={selectedCase || ({} as CaseInfo)}
          caseLoading={caseLoading}
          setSelectedCase={setSelectedCase}
        />
      )}

      {/* Precedents Result */}
      {activeTool?.id === 'precedents' && tr?.precedents && !selectedCase && !caseLoading && (
        <div className={`${styles.toolCard} ${styles.resultsList}`}>
          <div className={styles.toolOverline}>Precedents · CourtListener</div>
          <div className={styles.toolTitle}>"{toolInput}"</div>
          <div className={styles.resultsCount}>{tr.count || tr.precedents.length} cases found · most cited first</div>
          {tr.precedents.map((p, i) => {
            const meta = joinMeta([
              p.citation,
              `${(p.citation_count || p.times_cited || 0).toLocaleString()} citations`,
              p.court,
              p.date_filed,
              p.status && p.status !== 'Published' ? p.status : null,
            ]);
            const body = (
              <>
                <span className={styles.resultRank}>{i + 1}</span>
                <div className={styles.resultRowContent}>
                  <span className={styles.resultRowTitle}>{p.case_name}</span>
                  <span className={styles.resultRowMeta}>{meta}</span>
                  {(p.citations?.length ?? 0) > 1 && (
                    <span className={styles.resultRowMeta}>Also reported at {p.citations!.slice(1).join(', ')}</span>
                  )}
                  {p.snippet && <span className={styles.resultSnippet}>{p.snippet}</span>}
                </div>
              </>
            );
            return p.id ? (
              <div
                key={i}
                className={`${styles.resultRow} ${styles.resultRowLink}`}
                role="button"
                tabIndex={0}
                onClick={() => loadPrecedentCase(p.id!)}
                onKeyDown={(e) => { if (e.key === 'Enter') loadPrecedentCase(p.id!); }}
              >
                {body}
                {p.url && (
                  <a
                    href={safeHttpUrl(p.url) ?? undefined}
                    target="_blank"
                    rel="noopener noreferrer"
                    onClick={(e) => e.stopPropagation()}
                    title="View on CourtListener"
                  >
                    <Icon name="ExternalLink" size={14} className={styles.resultRowLinkIcon} />
                  </a>
                )}
              </div>
            ) : p.url ? (
              <a key={i} className={`${styles.resultRow} ${styles.resultRowLink}`} href={safeHttpUrl(p.url) ?? undefined} target="_blank" rel="noopener noreferrer">
                {body}
                <Icon name="ExternalLink" size={14} className={styles.resultRowLinkIcon} />
              </a>
            ) : (
              <div key={i} className={styles.resultRow}>{body}</div>
            );
          })}
        </div>
      )}

      {/* Docket Detail View — opens in-app; CourtListener link demoted to an icon */}
      {activeTool?.id === 'dockets' && (selectedDocket || docketLoading) && (
        <DocketDetailView
          styles={styles}
          docket={selectedDocket}
          docketLoading={docketLoading}
          onBack={() => setSelectedDocket(null)}
        />
      )}

      {/* Docket Search Result */}
      {activeTool?.id === 'dockets' && tr?.dockets && !selectedDocket && !docketLoading && (
        <div className={`${styles.toolCard} ${styles.resultsList}`}>
          <div className={styles.toolOverline}>Dockets · CourtListener</div>
          <div className={styles.toolTitle}>"{toolInput}"</div>
          <div className={styles.resultsCount}>{tr.count || tr.dockets.length} dockets found</div>
          {tr.dockets.map((d, i) => {
            const meta = joinMeta([
              d.court,
              d.docket_number,
              d.date_filed ? `Filed ${d.date_filed}` : null,
              d.date_terminated ? `Terminated ${d.date_terminated}` : null,
            ]);
            const detail = joinMeta([
              d.nature_of_suit,
              d.cause,
              d.assigned_to ? `Judge ${d.assigned_to}` : null,
              d.jury_demand && d.jury_demand !== 'None' ? `Jury: ${d.jury_demand}` : null,
            ]);
            const body = (
              <div className={styles.resultRowContent}>
                <span className={styles.resultRowTitle}>{d.case_name}</span>
                <span className={styles.resultRowMeta}>{meta}</span>
                {detail && <span className={styles.resultRowMeta}>{detail}</span>}
                {(d.parties?.length ?? 0) > 0 && (
                  <span className={styles.resultSnippet}>
                    {d.parties!.slice(0, 6).map((p) => p.name).join(' · ')}
                    {d.parties!.length > 6 ? ` · +${d.parties!.length - 6} more` : ''}
                  </span>
                )}
              </div>
            );
            return d.id ? (
              <div
                key={i}
                className={`${styles.resultRow} ${styles.resultRowLink}`}
                role="button"
                tabIndex={0}
                onClick={() => loadDocketDetail(d.id!)}
                onKeyDown={(e) => { if (e.key === 'Enter') loadDocketDetail(d.id!); }}
              >
                {body}
                {d.url && (
                  <a
                    href={safeHttpUrl(d.url) ?? undefined}
                    target="_blank"
                    rel="noopener noreferrer"
                    onClick={(e) => e.stopPropagation()}
                    title="View on CourtListener"
                  >
                    <Icon name="ExternalLink" size={14} className={styles.resultRowLinkIcon} />
                  </a>
                )}
              </div>
            ) : d.url ? (
              <a key={i} className={`${styles.resultRow} ${styles.resultRowLink}`} href={safeHttpUrl(d.url) ?? undefined} target="_blank" rel="noopener noreferrer">
                {body}
                <Icon name="ExternalLink" size={14} className={styles.resultRowLinkIcon} />
              </a>
            ) : (
              <div key={i} className={styles.resultRow}>{body}</div>
            );
          })}
        </div>
      )}

      {/* Oral Arguments Result */}
      {activeTool?.id === 'oral-arguments' && tr?.arguments && (
        <div className={`${styles.toolCard} ${styles.resultsList}`}>
          <div className={styles.toolOverline}>Oral Arguments · CourtListener</div>
          <div className={styles.toolTitle}>"{toolInput}"</div>
          <div className={styles.resultsCount}>{tr.count || tr.arguments.length} recordings found</div>
          <div className={styles.scrollableList}>
            {tr.arguments.map((a, i) => (
              <div key={i} className={styles.oralArgItem}>
                <div className={styles.resultRowContent}>
                  <span className={styles.resultRowTitle}>{a.case_name}</span>
                  <span className={styles.resultRowMeta}>
                    {joinMeta([
                      a.court,
                      a.date_argued ? `Argued ${a.date_argued}` : null,
                      a.docket_number,
                      a.duration_label,
                    ])}
                  </span>
                  {a.judges && <span className={styles.resultRowMeta}>Panel: {a.judges}</span>}
                  {a.snippet && <span className={styles.resultSnippet}>{a.snippet}</span>}
                </div>
                {a.audio_url ? (
                  <audio controls preload="none" className={styles.audioPlayer}>
                    <source src={a.audio_url} type="audio/mpeg" />
                    Your browser does not support audio.
                  </audio>
                ) : null}
                <div className={styles.oralArgLinks}>
                  {a.url && (
                    <a href={safeHttpUrl(a.url) ?? undefined} target="_blank" rel="noopener noreferrer" className={styles.oralArgLink}>
                      <Icon name="ExternalLink" size={12} /> {a.audio_url ? 'View on CourtListener' : 'Listen on CourtListener'}
                    </a>
                  )}
                  {a.download_url && a.download_url !== a.audio_url && (
                    <a href={safeHttpUrl(a.download_url) ?? undefined} target="_blank" rel="noopener noreferrer" className={styles.oralArgLink}>
                      <Icon name="Download" size={12} /> Court's original recording
                    </a>
                  )}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Trends Result */}
      {activeTool?.id === 'trends' && tr?.years && (
        <TrendChart
          styles={styles}
          topic={toolInput}
          years={tr.years}
          totalCases={tr.total_cases}
          trend={tr.trend}
          trendBasis={tr.trend_basis}
          currentYearPartial={tr.current_year_partial}
          yearsFailed={tr.years_failed}
        />
      )}
    </div>
  );
}

export { ToolResultsArea };
