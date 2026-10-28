import { Icon } from './Icon';
import type { ReviewCell, ReviewFieldSpec, ReviewTableDocument } from '../../api/types';

interface ReviewCellPopoverProps {
  cell: ReviewCell | null;
  field: ReviewFieldSpec | undefined;
  document: ReviewTableDocument | undefined;
  running: boolean;
  onRerun: () => void;
  onClose: () => void;
  styles: Record<string, string>;
}

const STATUS_LABEL: Record<string, string> = {
  extracted: 'Verified against the document',
  unverified: 'Unverified: the quote was not found in the document',
  not_found: 'Not found in the document',
  error: 'Extraction failed',
};

/** The evidence behind one cell: the value, the verbatim quote, where it sits, and its status. */
export function ReviewCellPopover({ cell, field, document, running, onRerun, onClose, styles }: ReviewCellPopoverProps) {
  if (!field || !document) return null;
  const status = cell?.status || 'pending';
  return (
    <aside className={styles.popover} aria-label="Cell details">
      <div className={styles.popoverHead}>
        <div>
          <div className={styles.popoverTitle}>{field.name}</div>
          <div className={styles.popoverSub}>{document.filename}</div>
        </div>
        <button type="button" className={styles.iconBtn} onClick={onClose} aria-label="Close details">
          <Icon name="X" size={16} />
        </button>
      </div>

      {!cell ? (
        <div className={styles.popoverEmpty}>Not extracted yet. Run the review to fill this cell.</div>
      ) : (
        <>
          <div className={`${styles.statusLine} ${styles[`status_${status}`] || ''}`}>
            <Icon
              name={status === 'extracted' ? 'CheckCircle' : status === 'error' ? 'XCircle' : 'AlertTriangle'}
              size={14}
            />
            {STATUS_LABEL[status] || status}
          </div>
          {cell.value && (
            <div className={styles.popoverSection}>
              <div className={styles.popoverLabel}>Value</div>
              <div className={styles.popoverValue}>{cell.value}</div>
            </div>
          )}
          {cell.quote && (
            <div className={styles.popoverSection}>
              <div className={styles.popoverLabel}>
                Quote{cell.span_start !== null && cell.span_end !== null ? ` · chars ${cell.span_start}–${cell.span_end}` : ''}
              </div>
              <blockquote className={styles.quote}>{cell.quote}</blockquote>
            </div>
          )}
          {cell.error && (
            <div className={styles.popoverSection}>
              <div className={styles.popoverLabel}>Error</div>
              <div className={styles.popoverError}>{cell.error}</div>
            </div>
          )}
          <div className={styles.popoverMeta}>
            {cell.model_used && <span>{cell.model_used}</span>}
            {cell.updated_at && <span>{new Date(cell.updated_at).toLocaleString()}</span>}
          </div>
        </>
      )}

      <div className={styles.popoverActions}>
        <button type="button" className={styles.secondaryBtn} onClick={onRerun} disabled={running || !document.accessible}>
          <Icon name="RefreshCw" size={13} /> Re-run this cell
        </button>
      </div>
    </aside>
  );
}
