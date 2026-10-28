import { Icon } from './Icon';
import { activateOnKey } from './activateOnKey';
import type { ReviewTableSummary } from '../../api/types';

interface ReviewTableCardProps {
  table: ReviewTableSummary;
  onOpen: (tableId: string) => void;
  styles: Record<string, string>;
}

/** One review table in the /review-tables list: name, scope size, last run. */
export function ReviewTableCard({ table, onOpen, styles }: ReviewTableCardProps) {
  const lastRun = table.last_run_at ? new Date(table.last_run_at).toLocaleString() : null;
  return (
    <div
      className={styles.card}
      role="button"
      tabIndex={0}
      onClick={() => onOpen(table.id)}
      onKeyDown={activateOnKey(() => onOpen(table.id))}
    >
      <div className={styles.cardIcon}>
        <Icon name="Table2" size={18} />
      </div>
      <div className={styles.cardBody}>
        <div className={styles.cardTitleRow}>
          <span className={styles.cardTitle}>{table.name}</span>
          <span className={`${styles.roleBadge} ${table.role === 'owner' ? styles.roleOwner : ''}`}>
            {table.role}
          </span>
        </div>
        {table.description && <div className={styles.cardDesc}>{table.description}</div>}
        <div className={styles.cardMeta}>
          <span>{table.document_count} document{table.document_count === 1 ? '' : 's'}</span>
          <span>{table.field_count} field{table.field_count === 1 ? '' : 's'}</span>
          <span>{lastRun ? `Last run ${lastRun}` : 'Not run yet'}</span>
        </div>
      </div>
    </div>
  );
}
