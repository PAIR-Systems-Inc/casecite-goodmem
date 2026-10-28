import { Icon } from './Icon';
import { cellKey, type CellRef } from '../../stores/reviewTablesStore';
import type { ReviewCell, ReviewFieldSpec, ReviewTableDocument } from '../../api/types';

interface ReviewResultsGridProps {
  fields: ReviewFieldSpec[];
  documents: ReviewTableDocument[];
  cells: Map<string, ReviewCell>;
  selected: CellRef | null;
  running: boolean;
  onSelect: (ref: CellRef | null) => void;
  onRerunRow: (documentId: string) => void;
  onRerunColumn: (fieldKey: string) => void;
  styles: Record<string, string>;
}

function cellText(cell: ReviewCell | undefined): string {
  if (!cell) return '';
  if (cell.status === 'not_found') return '—';
  if (cell.status === 'error') return 'error';
  return cell.value || '';
}

function cellClass(cell: ReviewCell | undefined, styles: Record<string, string>): string {
  if (!cell) return styles.cellEmpty;
  if (cell.status === 'extracted') return styles.cellVerified;
  if (cell.status === 'unverified') return styles.cellUnverified;
  if (cell.status === 'error') return styles.cellError;
  return styles.cellNotFound;
}

/** Documents down, fields across; every answered cell opens its quote. */
export function ReviewResultsGrid({
  fields, documents, cells, selected, running, onSelect, onRerunRow, onRerunColumn, styles,
}: ReviewResultsGridProps) {
  return (
    <div className={styles.gridWrap}>
      <table className={styles.grid}>
        <thead>
          <tr>
            <th className={`${styles.th} ${styles.thDoc}`}>Document</th>
            {fields.map((f) => (
              <th key={f.key || f.name} className={styles.th}>
                <div className={styles.thInner}>
                  <span className={styles.thName} title={f.description || undefined}>{f.name}</span>
                  <button
                    type="button"
                    className={styles.rerunBtn}
                    onClick={() => f.key && onRerunColumn(f.key)}
                    disabled={running || !f.key}
                    title={`Re-run "${f.name}" for every document`}
                    aria-label={`Re-run column ${f.name}`}
                  >
                    <Icon name="RefreshCw" size={11} />
                  </button>
                </div>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {documents.map((doc) => (
            <tr key={doc.id} className={doc.accessible ? undefined : styles.rowUnavailable}>
              <td className={`${styles.td} ${styles.tdDoc}`}>
                <div className={styles.tdDocInner}>
                  <Icon name={doc.accessible ? 'FileText' : 'FileX'} size={13} />
                  <span className={styles.docName} title={doc.filename}>{doc.filename}</span>
                  <button
                    type="button"
                    className={styles.rerunBtn}
                    onClick={() => onRerunRow(doc.id)}
                    disabled={running || !doc.accessible}
                    title="Re-run every field for this document"
                    aria-label={`Re-run row ${doc.filename}`}
                  >
                    <Icon name="RefreshCw" size={11} />
                  </button>
                </div>
              </td>
              {fields.map((f) => {
                const key = f.key || f.name;
                const cell = cells.get(cellKey(doc.id, key));
                const isSelected = selected?.documentId === doc.id && selected?.fieldKey === key;
                return (
                  <td
                    key={key}
                    className={`${styles.td} ${cellClass(cell, styles)} ${isSelected ? styles.cellSelected : ''}`}
                    onClick={() => onSelect(isSelected ? null : { documentId: doc.id, fieldKey: key })}
                    role="button"
                    tabIndex={0}
                    onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onSelect({ documentId: doc.id, fieldKey: key }); } }}
                    title={cell?.quote || undefined}
                  >
                    <span className={styles.cellValue}>{cellText(cell)}</span>
                    {cell?.status === 'extracted' && <Icon name="CheckCircle" size={11} className={styles.cellBadge} />}
                    {cell?.status === 'unverified' && <Icon name="AlertTriangle" size={11} className={styles.cellBadge} />}
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
