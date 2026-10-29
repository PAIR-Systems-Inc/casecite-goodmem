import React from 'react';
import { Icon } from './Icon';
import { activateOnKey } from './activateOnKey';
import type { JudgeSearchResult, JudgeSearchResponse } from '../../types';
// Reuse the Legal Tools floating-search styles so Judge Intel is visually
// identical to every other tool (centered hero pill -> results + bottom pill).
import t from '../../views/ToolsView.module.css';

interface JudgeSearchPanelProps {
  styles: Record<string, string>;
  judgeIntelSearch: string;
  setJudgeIntelSearch: (value: string) => void;
  searchJudgesIntel: () => void;
  judgeIntelLoading: boolean;
  judgeIntelResults: JudgeSearchResponse | null;
  buildJudgeIntel: (id: string, name: string) => void;
}

function JudgeSearchPanel({
  styles,
  judgeIntelSearch,
  setJudgeIntelSearch,
  searchJudgesIntel,
  judgeIntelLoading,
  judgeIntelResults,
  buildJudgeIntel,
}: JudgeSearchPanelProps) {
  const hasResults = !!judgeIntelResults?.judges;

  return (
    <div className={t.toolsView}>
      {!hasResults ? (
        <div className={t.hero}>
          <h1 className={t.heroTitle}>Judge Intelligence</h1>
          <p className={t.heroSubtitle}>Search any judge for their full profile and analytics.</p>
          <div className={t.searchPill}>
            <input
              type="text"
              className={t.pillInput}
              placeholder="Enter judge name (e.g., Sotomayor, Roberts, Anderson)"
              aria-label="Judge name"
              value={judgeIntelSearch}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => setJudgeIntelSearch(e.target.value)}
              onKeyDown={(e: React.KeyboardEvent<HTMLInputElement>) => e.key === 'Enter' && judgeIntelSearch.trim() && searchJudgesIntel()}
              autoFocus
            />
            <button
              className={t.pillBtn}
              onClick={searchJudgesIntel}
              disabled={judgeIntelLoading || !judgeIntelSearch.trim()}
              aria-label="Search judges"
            >
              {judgeIntelLoading ? <Icon name="Loader2" size={16} className={t.spin} /> : <Icon name="Search" size={16} />}
            </button>
          </div>
          {judgeIntelResults?.error && (
            <div className={styles.toolErrorBox}><Icon name="AlertCircle" size={14} /> {judgeIntelResults.error}</div>
          )}
        </div>
      ) : (
        <>
          <div className={t.resultsScroll}>
            <div className={styles.judgeIntelResultsList}>
              <div className={styles.judgeIntelResultsHeader}>
                Showing {judgeIntelResults.count ?? 0} judges
                {(judgeIntelResults.total_available ?? 0) > (judgeIntelResults.count ?? 0) &&
                  ` (${(judgeIntelResults.total_available ?? 0).toLocaleString()} total in database)`}
              </div>
              {judgeIntelResults.judges!.map((j: JudgeSearchResult, i: number) => (
                <div
                  key={`${j.id}-${i}`}
                  className={styles.judgeIntelResultItem}
                  role="button"
                  tabIndex={0}
                  onClick={() => buildJudgeIntel(j.id, j.name)}
                  onKeyDown={activateOnKey(() => buildJudgeIntel(j.id, j.name))}
                >
                  <div className={styles.judgeIntelResultIcon}>
                    <Icon name="User" size={20} />
                  </div>
                  <div className={styles.judgeIntelResultContent}>
                    <div className={styles.judgeIntelResultName}>{j.name || j.name_full}</div>
                    <div className={styles.judgeIntelResultMeta}>
                      {j.court && <span><Icon name="Building" size={12} /> {j.court}</span>}
                      {j.position && <span><Icon name="Briefcase" size={12} /> {j.position}</span>}
                      {j.appointed_by && <span><Icon name="Award" size={12} /> {j.appointed_by}</span>}
                    </div>
                  </div>
                  <Icon name="ChevronRight" size={20} className={styles.resultChevron} />
                </div>
              ))}
            </div>
          </div>

          {/* Floating composer at the bottom — refine the judge search */}
          <div className={t.bottomBar}>
            <div className={t.searchPill}>
              <input
                type="text"
                className={t.pillInput}
                placeholder="Search another judge..."
                aria-label="Search another judge"
                value={judgeIntelSearch}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => setJudgeIntelSearch(e.target.value)}
                onKeyDown={(e: React.KeyboardEvent<HTMLInputElement>) => e.key === 'Enter' && judgeIntelSearch.trim() && searchJudgesIntel()}
              />
              <button
                className={t.pillBtn}
                onClick={searchJudgesIntel}
                disabled={judgeIntelLoading || !judgeIntelSearch.trim()}
                aria-label="Search judges"
              >
                {judgeIntelLoading ? <Icon name="Loader2" size={16} className={t.spin} /> : <Icon name="Search" size={16} />}
              </button>
            </div>
          </div>
        </>
      )}
    </div>
  );
}

export { JudgeSearchPanel };
export default JudgeSearchPanel;
