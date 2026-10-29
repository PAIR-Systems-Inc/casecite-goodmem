import { useEffect, useMemo, useState } from 'react';
import { Icon } from './Icon';
import type { RagDocument } from '../../types';

interface ReviewDocumentPickerProps {
  isOpen: boolean;
  documents: RagDocument[];
  loading: boolean;
  /** Documents currently in the table's scope. */
  selectedIds: string[];
  scopeLabel: string;
  onConfirm: (documentIds: string[]) => Promise<boolean>;
  onClose: () => void;
  styles: Record<string, string>;
}

/**
 * Choose the documents a review runs over. Unlike the 4-file contract picker
 * this is a checklist with search and select-all: a review sweeps hundreds.
 */
export function ReviewDocumentPicker({
  isOpen, documents, loading, selectedIds, scopeLabel, onConfirm, onClose, styles,
}: ReviewDocumentPickerProps) {
  const [query, setQuery] = useState('');
  const [selected, setSelected] = useState<Set<string>>(new Set(selectedIds));
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (isOpen) { setSelected(new Set(selectedIds)); setQuery(''); }
  }, [isOpen, selectedIds]);

  const indexed = useMemo(() => documents.filter((d) => d.status === 'indexed'), [documents]);
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    return q ? indexed.filter((d) => d.filename.toLowerCase().includes(q) || (d.folder_path || '').toLowerCase().includes(q)) : indexed;
  }, [indexed, query]);

  if (!isOpen) return null;

  const toggle = (id: string) => setSelected((prev) => {
    const next = new Set(prev);
    if (next.has(id)) next.delete(id); else next.add(id);
    return next;
  });
  const selectVisible = (on: boolean) => setSelected((prev) => {
    const next = new Set(prev);
    for (const d of visible) { if (on) next.add(d.id); else next.delete(d.id); }
    return next;
  });
  const allVisibleOn = visible.length > 0 && visible.every((d) => selected.has(d.id));

  const submit = async () => {
    setSaving(true);
    // Keep the order documents already had, then append new picks.
    const ordered = [...selectedIds.filter((id) => selected.has(id)), ...[...selected].filter((id) => !selectedIds.includes(id))];
    const ok = await onConfirm(ordered);
    setSaving(false);
    if (ok) onClose();
  };

  return (
    <div className={styles.pickerBackdrop} onClick={onClose} role="presentation">
      <div className={styles.picker} onClick={(e) => e.stopPropagation()} role="dialog" aria-label="Choose documents">
        <div className={styles.pickerHead}>
          <div>
            <div className={styles.pickerTitle}>Documents to review</div>
            <div className={styles.pickerSub}>{scopeLabel}</div>
          </div>
          <button type="button" className={styles.iconBtn} onClick={onClose} aria-label="Close">
            <Icon name="X" size={16} />
          </button>
        </div>
        <div className={styles.pickerTools}>
          <input
            className={styles.input}
            placeholder="Filter by name or folder"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            aria-label="Filter documents"
            autoFocus
          />
          <button type="button" className={styles.secondaryBtn} onClick={() => selectVisible(!allVisibleOn)} disabled={visible.length === 0}>
            {allVisibleOn ? 'Clear shown' : `Select shown (${visible.length})`}
          </button>
        </div>
        <div className={styles.pickerList}>
          {loading ? (
            <div className={styles.pickerEmpty}><Icon name="Loader2" size={14} className={styles.spin} /> Loading documents…</div>
          ) : visible.length === 0 ? (
            <div className={styles.pickerEmpty}>No indexed documents match.</div>
          ) : (
            visible.map((d) => (
              <label key={d.id} className={styles.pickerItem}>
                <input type="checkbox" checked={selected.has(d.id)} onChange={() => toggle(d.id)} />
                <Icon name="FileText" size={14} />
                <span className={styles.pickerName}>{d.filename}</span>
                {d.folder_path && <span className={styles.pickerFolder}>{d.folder_path}</span>}
              </label>
            ))
          )}
        </div>
        <div className={styles.pickerFoot}>
          <span className={styles.pickerCount}>{selected.size} selected</span>
          <span className={styles.spacer} />
          <button type="button" className={styles.secondaryBtn} onClick={onClose}>Cancel</button>
          <button type="button" className={styles.primaryBtn} onClick={submit} disabled={saving}>
            <Icon name={saving ? 'Loader2' : 'Check'} size={14} className={saving ? styles.spin : undefined} />
            Use {selected.size} document{selected.size === 1 ? '' : 's'}
          </button>
        </div>
      </div>
    </div>
  );
}
