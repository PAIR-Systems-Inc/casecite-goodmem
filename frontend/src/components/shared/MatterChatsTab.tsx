import { Icon } from './Icon';
import type { ChatSessionSummary } from '../../api/types';

interface MatterChatsTabProps {
  chats: ChatSessionSummary[];
  loading: boolean;
  currentUserId: string | null;
  onOpen: (sessionId: string) => void;
  styles: Record<string, string>;
}

/** Research threads filed under the matter, the user's own and colleagues'. */
export function MatterChatsTab({ chats, loading, currentUserId, onOpen, styles }: MatterChatsTabProps) {
  if (loading && chats.length === 0) {
    return <div className={styles.empty}><Icon name="Loader2" size={16} className={styles.spin} /> Loading chats…</div>;
  }
  if (chats.length === 0) {
    return (
      <div className={styles.empty}>
        <Icon name="MessageSquare" size={18} />
        <p>No research chats are filed under this matter yet.</p>
      </div>
    );
  }
  return (
    <ul className={styles.list}>
      {chats.map((chat) => (
        <li key={chat.id} className={styles.row}>
          <Icon name="MessageSquare" size={16} className={styles.rowIcon} />
          <div className={styles.rowBody}>
            <div className={styles.rowName}>{chat.title || 'Untitled conversation'}</div>
            <div className={styles.rowMeta}>
              {chat.message_count} message{chat.message_count === 1 ? '' : 's'}
              {chat.updated_at ? ` · ${new Date(chat.updated_at).toLocaleString()}` : ''}
            </div>
          </div>
          <span className={`${styles.ownerBadge} ${chat.user_id === currentUserId ? styles.ownerBadgeMine : ''}`}>
            {chat.user_id === currentUserId ? 'Yours' : 'Colleague'}
          </span>
          <button type="button" className={styles.toolBtn} onClick={() => onOpen(chat.id)}>
            <Icon name="ExternalLink" size={13} /> Open
          </button>
        </li>
      ))}
    </ul>
  );
}
