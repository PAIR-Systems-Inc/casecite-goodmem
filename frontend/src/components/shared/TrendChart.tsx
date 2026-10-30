import { Icon } from './Icon';
import type { TrendYear } from './ToolResultsArea';

interface TrendChartProps {
  styles: Record<string, string>;
  topic: string;
  years: TrendYear[];
  totalCases?: number;
  trend?: string;
  trendBasis?: { from_year: number; to_year: number };
  currentYearPartial?: boolean;
  yearsFailed?: number;
}

const TREND_LABEL: Record<string, { icon: string; label: string }> = {
  increasing: { icon: 'TrendingUp', label: 'Increasing' },
  decreasing: { icon: 'TrendingDown', label: 'Decreasing' },
  stable: { icon: 'Minus', label: 'Stable' },
  insufficient: { icon: 'Minus', label: 'Too few years' },
};

function TrendChart({ styles, topic, years, totalCases, trend, trendBasis, currentYearPartial, yearsFailed }: TrendChartProps) {
  // A mostly failed sweep cannot establish a trend. An unavailable year may
  // reflect an outage or timeout as well as rate limiting.
  if (years.length < 3 && (yearsFailed ?? 0) > years.length) {
    return (
      <div className={styles.trendCard}>
        <div className={styles.trendOverline}>Case-law Trend · CourtListener</div>
        <div className={styles.trendTitle}>"{topic}"</div>
        <div className={styles.trendNote}>
          <Icon name="AlertCircle" size={12} /> CourtListener yearly counts could not be retrieved
          ({yearsFailed} of {years.length + (yearsFailed ?? 0)} years unavailable). Wait a minute and run the search again.
        </div>
      </div>
    );
  }
  if (years.length === 0) return null;

  const maxCount = Math.max(...years.map((yr) => yr.count), 1);
  const lastYear = years[years.length - 1].year;
  const currentYear = new Date().getFullYear();
  const completeYears = years.filter((y) => y.year < currentYear);
  const peakYears = completeYears.length ? completeYears : years;
  const peak = peakYears.reduce((a, b) => (b.count > a.count ? b : a), peakYears[0]);
  const total = totalCases ?? years.reduce((sum: number, y) => sum + y.count, 0);
  // Keep ~9 x-labels regardless of span; the last year is always labeled.
  const labelStep = Math.max(1, Math.ceil(years.length / 9));
  const trendInfo = TREND_LABEL[trend || ''] || TREND_LABEL.stable;

  return (
    <div className={styles.trendCard}>
      <div className={styles.trendOverline}>Case-law Trend · CourtListener</div>
      <div className={styles.trendTitle}>"{topic}"</div>

      <div className={styles.trendStatsRow}>
        <div className={styles.trendStat}>
          <span className={`${styles.trendStatValue} mono`}>{total.toLocaleString()}</span>
          <span className={styles.trendStatLabel}>Cases {years[0].year}–{lastYear}</span>
        </div>
        <div className={styles.trendStat}>
          <span className={`${styles.trendStatValue} mono`}>{peak.year}</span>
          <span className={styles.trendStatLabel}>Peak · {peak.count.toLocaleString()} cases</span>
        </div>
        <div className={styles.trendStat}>
          <span className={styles.trendStatValue}>
            <Icon name={trendInfo.icon} size={16} />
          </span>
          <span className={styles.trendStatLabel}>
            {trendInfo.label}
            {trendBasis && trend !== 'insufficient' ? ` · ${trendBasis.from_year}→${trendBasis.to_year}` : ''}
          </span>
        </div>
      </div>

      <div className={styles.trendBars}>
        {years.map((y, i) => {
          const partial = currentYearPartial && y.year === currentYear;
          return (
            <div key={i} className={styles.trendBarCol}>
              <div className={styles.trendTip}>{y.year} · {y.count.toLocaleString()} cases{partial ? ' so far' : ''}</div>
              <div
                className={`${styles.trendBarFill} ${y.count > 0 ? styles.trendBarFillActive : ''} ${partial ? styles.trendBarFillPartial : ''}`}
                style={{ height: `${(y.count / maxCount) * 128}px` }}
              />
              <span className={`${styles.trendBarYear} mono`}>
                {(i % labelStep === 0 || i === years.length - 1) ? y.year : ''}
              </span>
            </div>
          );
        })}
      </div>

      {currentYearPartial && (
        <div className={styles.trendNote}>
          <Icon name="Info" size={11} /> {currentYear} is still in progress; it is shown but not counted toward the trend.
        </div>
      )}
      {(yearsFailed ?? 0) > 0 && (
        <div className={styles.trendNote}>
          <Icon name="AlertCircle" size={11} /> {yearsFailed} year{yearsFailed !== 1 ? 's' : ''} could not be counted and {yearsFailed !== 1 ? 'are' : 'is'} not shown.
        </div>
      )}
    </div>
  );
}

export { TrendChart };
