import { Icon } from './Icon';
import { contractTypeLabel } from '../../utils';
import type { MatterAnalysisItem } from '../../api/types';

interface MatterAnalysesTabProps {
  analyses: MatterAnalysisItem[];
  loading: boolean;
  currentUserId: string | null;
  onOpen: (analysisId: string) => void;
  styles: Record<string, string>;
}

/** Contract analyses filed under the matter. */
export function MatterAnalysesTab({ analyses, loading, currentUserId, onOpen, styles }: MatterAnalysesTabProps) {
  if (loading && analyses.length === 0) {
    return <div className={styles.empty}><Icon name="Loader2" size={16} className={styles.spin} /> Loading analyses…</div>;
  }
  if (analyses.length === 0) {
    return (
      <div className={styles.empty}>
        <Icon name="FileCheck" size={18} />
        <p>No contract analyses are filed under this matter yet.</p>
      </div>
    );
  }
  return (
    <ul className={styles.list}>
      {analyses.map((a) => (
        <li key={a.analysis_id} className={styles.row}>
          <Icon name="FileCheck" size={16} className={styles.rowIcon} />
          <div className={styles.rowBody}>
            <div className={styles.rowName}>{a.filename || 'Pasted contract text'}</div>
            <div className={styles.rowMeta}>
              {a.contract_type ? contractTypeLabel(a.contract_type) : 'Contract'}
              {` · ${a.issues} issue${a.issues === 1 ? '' : 's'}`}
              {a.created_at ? ` · ${new Date(a.created_at).toLocaleString()}` : ''}
            </div>
          </div>
          <span className={`${styles.ownerBadge} ${a.user_id === currentUserId ? styles.ownerBadgeMine : ''}`}>
            {a.user_id === currentUserId ? 'Yours' : 'Colleague'}
          </span>
          <button type="button" className={styles.toolBtn} onClick={() => onOpen(a.analysis_id)}>
            <Icon name="ExternalLink" size={13} /> Open
          </button>
        </li>
      ))}
    </ul>
  );
}
