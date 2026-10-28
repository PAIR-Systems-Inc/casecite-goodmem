import { Icon } from './Icon';
import type { MatterActivityEvent } from '../../api/types';

interface MatterActivityTabProps {
  events: MatterActivityEvent[];
  loading: boolean;
  styles: Record<string, string>;
}

/** "matter_member_added" -> "Matter member added". */
const describe = (event: MatterActivityEvent): string => {
  const raw = event.action || event.event_type || 'activity';
  const text = raw.replace(/_/g, ' ');
  return text.charAt(0).toUpperCase() + text.slice(1);
};

const iconFor = (event: MatterActivityEvent): string => {
  if (!event.success) return 'AlertTriangle';
  switch (event.resource_type) {
    case 'document': return 'FileText';
    case 'contract_analysis': return 'FileCheck';
    case 'chat': return 'MessageSquare';
    default: return 'Briefcase';
  }
};

/** The audit trail entries about the matter and what is filed under it, newest first. */
export function MatterActivityTab({ events, loading, styles }: MatterActivityTabProps) {
  if (loading && events.length === 0) {
    return <div className={styles.empty}><Icon name="Loader2" size={16} className={styles.spin} /> Loading activity…</div>;
  }
  if (events.length === 0) {
    return (
      <div className={styles.empty}>
        <Icon name="History" size={18} />
        <p>No recorded activity for this matter yet.</p>
      </div>
    );
  }
  return (
    <ul className={styles.list}>
      {events.map((event) => (
        <li key={event.id} className={`${styles.row} ${event.success ? '' : styles.rowFailed}`}>
          <Icon name={iconFor(event)} size={16} className={event.success ? styles.rowIcon : styles.rowIconError} />
          <div className={styles.rowBody}>
            <div className={styles.rowName}>
              {describe(event)}
              {!event.success && <span className={styles.failedBadge}>failed</span>}
            </div>
            <div className={styles.rowMeta}>
              {new Date(event.timestamp).toLocaleString()}
              {event.user_email ? ` · ${event.user_email}` : ''}
              {event.resource_type && event.resource_type !== 'matter' ? ` · ${event.resource_type.replace(/_/g, ' ')}` : ''}
            </div>
          </div>
        </li>
      ))}
    </ul>
  );
}
