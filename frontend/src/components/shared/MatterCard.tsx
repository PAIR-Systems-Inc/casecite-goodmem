import { Icon } from './Icon';
import { activateOnKey } from './activateOnKey';
import type { Matter } from '../../api/types';

interface MatterCardProps {
  matter: Matter;
  isActive: boolean;
  onOpen: (matterId: string) => void;
  onSetActive: (matterId: string | null) => void;
  styles: Record<string, string>;
}

/** One matter in the /matters list: name, client, role and a quick scope toggle. */
export function MatterCard({ matter, isActive, onOpen, onSetActive, styles }: MatterCardProps) {
  const name = matter.is_personal ? 'Personal' : matter.name;
  return (
    <div
      className={`${styles.card} ${isActive ? styles.cardActive : ''}`}
      role="button"
      tabIndex={0}
      onClick={() => onOpen(matter.id)}
      onKeyDown={activateOnKey(() => onOpen(matter.id))}
    >
      <div className={styles.cardIcon}>
        <Icon name={matter.is_personal ? 'User' : 'Briefcase'} size={18} />
      </div>
      <div className={styles.cardBody}>
        <div className={styles.cardTitleRow}>
          <span className={styles.cardTitle}>{name}</span>
          <span className={`${styles.roleBadge} ${matter.role === 'owner' ? styles.roleOwner : ''}`}>
            {matter.is_personal ? 'private' : matter.role}
          </span>
        </div>
        <div className={styles.cardMeta}>
          {matter.client_name && <span>{matter.client_name}</span>}
          {!matter.is_personal && (
            <span>{matter.member_count} member{matter.member_count === 1 ? '' : 's'}</span>
          )}
          <span>Created {new Date(matter.created_at).toLocaleDateString()}</span>
        </div>
      </div>
      <button
        type="button"
        className={`${styles.scopeBtn} ${isActive ? styles.scopeBtnActive : ''}`}
        onClick={(e) => { e.stopPropagation(); onSetActive(isActive ? null : matter.id); }}
        title={isActive ? 'Stop scoping the workspace to this matter' : 'Scope Research, Contracts and Drafting to this matter'}
      >
        <Icon name={isActive ? 'CheckCircle2' : 'Target'} size={13} />
        {isActive ? 'Active' : 'Set active'}
      </button>
    </div>
  );
}
