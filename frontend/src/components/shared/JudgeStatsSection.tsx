import React from 'react';
import type { JudgeStats } from '../../types';

interface JudgeStatsSectionProps {
  styles: Record<string, string>;
  judgeIntelStats: JudgeStats | null;
}

function JudgeStatsSection({
  styles,
  judgeIntelStats,
}: JudgeStatsSectionProps) {
  if (!judgeIntelStats) return null;

  const overview = judgeIntelStats.overview;
  const days = judgeIntelStats.by_day_of_week ?? [];
  const distribution = judgeIntelStats.citation_distribution ?? [];
  if (!overview && days.length === 0 && distribution.length === 0) return null;

  return (
    <div className={styles.judgeProfileSection}>
      <h3 className={styles.judgeProfileSectionTitle}>Statistics</h3>

      {overview && (
        <div className={styles.statChipRow}>
          {[
            { label: 'Avg Citations/Opinion', value: overview.avg_citations_per_opinion?.toFixed(1) ?? '—' },
            { label: 'Avg Words/Opinion', value: Math.round(overview.avg_opinion_length || 0).toLocaleString() },
          ].map((m, i) => (
            <div key={i} className={styles.statChip}>
              <span className={`${styles.statChipValue} mono`}>{m.value}</span>
              <span className={styles.statChipLabel}>{m.label}</span>
            </div>
          ))}
        </div>
      )}

      {days.length > 0 && (
        <div className={styles.statSubBlock}>
          <div className={styles.statSubTitle}>Opinions by Day of Week</div>
          <div className={styles.statsDayGrid}>
            {days.map((d, i: number) => (
              <div key={i} className={styles.statsDayItem}>
                <span className={styles.statsDayName}>{d.day_of_week?.slice(0, 3)}</span>
                <span className={`${styles.statsDayCount} mono`}>{d.opinions}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {distribution.length > 0 && (
        <div className={styles.statSubBlock}>
          <div className={styles.statSubTitle}>Citation Distribution</div>
          <div className={styles.mixBars}>
            {(() => {
              const maxCount = Math.max(...distribution.map((c) => c.count));
              return distribution.map((c, i: number) => (
                <div key={i} className={styles.mixBarRow}>
                  <span className={styles.mixBarLabel}>{c.citation_range}</span>
                  <div className={styles.mixBarTrack}>
                    <div className={styles.mixBarFill} style={{ '--bar-width': `${maxCount > 0 ? (c.count / maxCount) * 100 : 0}%` } as React.CSSProperties} />
                  </div>
                  <span className={`${styles.mixBarCount} mono`}>{c.count.toLocaleString()}</span>
                </div>
              ));
            })()}
          </div>
        </div>
      )}
    </div>
  );
}

export { JudgeStatsSection };
export default JudgeStatsSection;
