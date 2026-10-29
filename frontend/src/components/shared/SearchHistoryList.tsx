import { Icon } from './Icon';
import { parseUtcDate } from '../../utils';
import type { ChatSessionSummary } from '../../api/types';

interface SearchHistoryListProps {
  styles: Record<string, string>;
  sessions: ChatSessionSummary[];
  isLoading: boolean;
  currentSessionId: string | null;
  onSessionClick: (sessionId: string) => void;
  onDeleteSession: (sessionId: string) => void;
  onNewChat: () => void;
}

function SearchHistoryList({ styles, sessions, isLoading, currentSessionId, onSessionClick, onDeleteSession, onNewChat }: SearchHistoryListProps) {
  return (
    <div className={styles.rightSection}>
      <h4 className={styles.rightSectionTitle}>Conversations ({sessions.length})</h4>
      <button className={styles.newChatBtn} onClick={onNewChat}>
        <Icon name="Plus" size={14} /> New conversation
      </button>
      {isLoading && sessions.length === 0 ? (
        <div className={styles.emptyPanel}>
          <Icon name="Loader2" size={24} className={styles.emptyPanelIcon} />
          <div>Loading conversations...</div>
        </div>
      ) : sessions.length === 0 ? (
        <div className={styles.emptyPanel}>
          <Icon name="History" size={24} className={styles.emptyPanelIcon} />
          <div>No conversations yet</div>
          <div className={styles.emptyPanelDesc}>
            Your past conversations will appear here
          </div>
        </div>
      ) : (
        <div className={styles.historyList}>
          {sessions.map((session: ChatSessionSummary) => (
            <div key={session.id} className={styles.historyRow}>
              <button
                className={`${styles.historyItem} ${session.id === currentSessionId ? styles.historyItemActive : ''}`}
                onClick={() => onSessionClick(session.id)}
              >
                <Icon name="MessageSquare" size={14} className={styles.iconGray400} />
                <div className={styles.historyContent}>
                  <span className={styles.historyQuery}>{session.title}</span>
                  <span className={styles.historyMeta}>
                    {session.message_count} messages • {parseUtcDate(session.updated_at).toLocaleDateString()}
                  </span>
                </div>
              </button>
              <button
                className={styles.historyDeleteBtn}
                onClick={() => onDeleteSession(session.id)}
                title="Delete conversation"
                aria-label={`Delete conversation ${session.title}`}
              >
                <Icon name="Trash2" size={12} />
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export { SearchHistoryList };
