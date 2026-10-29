import { useState } from 'react';
import { Icon } from './Icon';
import type { MatterMember } from '../../api/types';

interface MatterMembersTabProps {
  members: MatterMember[];
  isOwner: boolean;
  isPersonal: boolean;
  currentUserId: string | null;
  onAdd: (email: string) => Promise<boolean>;
  onRemove: (userId: string, label: string) => void;
  onTransfer: (userId: string, label: string) => void;
  styles: Record<string, string>;
}

const memberLabel = (m: MatterMember) => m.name || m.email || m.user_id;

/** Who can see the matter; the owner invites by email, removes, or hands the matter over. */
export function MatterMembersTab({
  members, isOwner, isPersonal, currentUserId, onAdd, onRemove, onTransfer, styles,
}: MatterMembersTabProps) {
  const [email, setEmail] = useState('');
  const [adding, setAdding] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email.trim()) return;
    setAdding(true);
    const ok = await onAdd(email);
    setAdding(false);
    if (ok) setEmail('');
  };

  if (isPersonal) {
    return (
      <div className={styles.empty}>
        <Icon name="Lock" size={18} />
        <p>Your personal matter is private. Create a shared matter to work with colleagues.</p>
      </div>
    );
  }

  return (
    <div>
      {isOwner && (
        <form className={styles.inviteForm} onSubmit={submit}>
          <input
            type="email"
            className={styles.input}
            placeholder="colleague@firm.com"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            aria-label="Colleague's email address"
          />
          <button type="submit" className={styles.toolBtnPrimary} disabled={adding || !email.trim()}>
            <Icon name={adding ? 'Loader2' : 'UserPlus'} size={13} className={adding ? styles.spin : undefined} />
            Add member
          </button>
          <span className={styles.hint}>They need an active account on this instance.</span>
        </form>
      )}
      <ul className={styles.list}>
        {members.map((m) => {
          const isSelf = m.user_id === currentUserId;
          const label = memberLabel(m);
          return (
            <li key={m.user_id} className={styles.row}>
              <div className={styles.avatar}>{(m.name || m.email || '?')[0]?.toUpperCase()}</div>
              <div className={styles.rowBody}>
                <div className={styles.rowName}>
                  {label}{isSelf ? ' (you)' : ''}
                </div>
                {m.email && m.name && <div className={styles.rowMeta}>{m.email}</div>}
              </div>
              <span className={`${styles.roleBadge} ${m.role === 'owner' ? styles.roleOwner : ''}`}>{m.role}</span>
              {isOwner && m.role !== 'owner' && (
                <>
                  <button type="button" className={styles.toolBtn} onClick={() => onTransfer(m.user_id, label)} title="Make this member the owner">
                    <Icon name="KeyRound" size={13} /> Make owner
                  </button>
                  <button type="button" className={styles.toolBtnDanger} onClick={() => onRemove(m.user_id, label)} title="Remove from matter">
                    <Icon name="UserMinus" size={13} /> Remove
                  </button>
                </>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
