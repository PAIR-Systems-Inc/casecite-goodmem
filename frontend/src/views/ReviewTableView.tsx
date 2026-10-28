import { useEffect, useMemo, useState } from 'react';
import { useParams } from 'react-router-dom';
import { useReviewTablesStore, indexCells, cellKey, STANDARD_CONTRACT_FIELDS } from '../stores/reviewTablesStore';
import { useMattersStore } from '../stores/mattersStore';
import { appNavigate } from '../utils/router';
import {
  Icon,
  ReviewSchemaEditor,
  ReviewDocumentPicker,
  ReviewResultsGrid,
  ReviewCellPopover,
  ReviewRunBar,
} from '../components';
import type { ReviewFieldSpec } from '../api/types';
import s from './ReviewTableView.module.css';

/** One review table: its schema and documents, the results grid, and the evidence behind each cell. */
function ReviewTableView() {
  const { id } = useParams<{ id: string }>();
  const {
    table, tableError, loadTable, estimate,
    saveDetails, saveFields, saveDocuments, deleteTable,
    running, progress, runError, run,
    exporting, exportTable,
    selectedCell, selectCell, schemaOpen, setSchemaOpen,
    pickerOpen, pickerDocs, pickerLoading, openPicker, closePicker,
  } = useReviewTablesStore();
  const activeMatter = useMattersStore((st) => st.activeMatter());
  const mattersInit = useMattersStore((st) => st.init);

  useEffect(() => { mattersInit(); }, [mattersInit]);
  useEffect(() => { if (id) loadTable(id); }, [id, loadTable]);

  // Local UI state: the schema draft while the editor is open, and the rename form.
  const [draftFields, setDraftFields] = useState<ReviewFieldSpec[]>([]);
  const [renaming, setRenaming] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  useEffect(() => { if (schemaOpen && table) setDraftFields(table.fields); }, [schemaOpen, table]);

  const cells = useMemo(() => indexCells(table?.cells || []), [table]);
  const isOwner = table?.role === 'owner';
  const selected = selectedCell ? cells.get(cellKey(selectedCell.documentId, selectedCell.fieldKey)) || null : null;
  const selectedField = selectedCell ? table?.fields.find((f) => f.key === selectedCell.fieldKey) : undefined;
  const selectedDoc = selectedCell ? table?.documents.find((d) => d.id === selectedCell.documentId) : undefined;

  const startRename = () => {
    if (!table) return;
    setName(table.name); setDescription(table.description || ''); setRenaming(true);
  };
  const submitRename = async (e: React.FormEvent) => {
    e.preventDefault();
    if (await saveDetails(name, description)) setRenaming(false);
  };
  const submitSchema = async () => {
    if (await saveFields(draftFields)) setSchemaOpen(false);
  };
  const addStandard = () =>
    setDraftFields((prev) => [...prev, ...STANDARD_CONTRACT_FIELDS.filter((f) => !prev.some((p) => p.name.toLowerCase() === f.name.toLowerCase()))]);

  if (tableError) {
    return (
      <div className={s.container}>
        <div className={s.content}>
          <button className={s.backBtn} onClick={() => appNavigate('/review-tables')}>
            <Icon name="ArrowLeft" size={14} /> All reviews
          </button>
          <div className={s.empty}><Icon name="AlertCircle" size={18} /><p>{tableError}</p></div>
        </div>
      </div>
    );
  }

  if (!table) {
    return (
      <div className={s.container}>
        <div className={s.content}>
          <div className={s.empty}><Icon name="Loader2" size={16} className={s.spin} /> Loading review…</div>
        </div>
      </div>
    );
  }

  const canRun = table.documents.some((d) => d.accessible) && table.fields.length > 0;
  const unavailable = table.documents.filter((d) => !d.accessible).length;

  return (
    <div className={s.container}>
      <div className={s.content}>
        <button className={s.backBtn} onClick={() => appNavigate('/review-tables')}>
          <Icon name="ArrowLeft" size={14} /> All reviews
        </button>

        <div className={s.headerRow}>
          {renaming ? (
            <form className={s.renameForm} onSubmit={submitRename}>
              <input className={s.input} value={name} onChange={(e) => setName(e.target.value)} aria-label="Review name" autoFocus />
              <input className={`${s.input} ${s.inputWide}`} value={description} onChange={(e) => setDescription(e.target.value)} placeholder="Description" aria-label="Description" />
              <button type="submit" className={s.primaryBtn} disabled={!name.trim()}><Icon name="Check" size={14} /> Save</button>
              <button type="button" className={s.secondaryBtn} onClick={() => setRenaming(false)}>Cancel</button>
            </form>
          ) : (
            <div className={s.titleBlock}>
              <div className={s.titleRow}>
                <h2 className={s.title}>{table.name}</h2>
                <span className={`${s.roleBadge} ${isOwner ? s.roleOwner : ''}`}>{table.role}</span>
                {isOwner && (
                  <button type="button" className={s.iconBtn} onClick={startRename} title="Rename" aria-label="Rename review">
                    <Icon name="Pencil" size={13} />
                  </button>
                )}
              </div>
              {table.description && <p className={s.subtitle}>{table.description}</p>}
              <div className={s.metaRow}>
                <span>{table.documents.length} document{table.documents.length === 1 ? '' : 's'}</span>
                <span>{table.fields.length} field{table.fields.length === 1 ? '' : 's'}</span>
                <span>{table.cell_count} cell{table.cell_count === 1 ? '' : 's'} extracted</span>
                {table.last_run_at && <span>Last run {new Date(table.last_run_at).toLocaleString()}</span>}
                {unavailable > 0 && <span className={s.warn}>{unavailable} document{unavailable === 1 ? '' : 's'} no longer readable</span>}
              </div>
            </div>
          )}
          <div className={s.headerActions}>
            <button className={s.secondaryBtn} onClick={openPicker} disabled={!isOwner || running} title={isOwner ? undefined : 'Only the owner changes the documents'}>
              <Icon name="Files" size={14} /> Documents
            </button>
            <button className={`${s.secondaryBtn} ${schemaOpen ? s.secondaryBtnActive : ''}`} onClick={() => setSchemaOpen(!schemaOpen)} disabled={!isOwner || running} title={isOwner ? undefined : 'Only the owner changes the fields'}>
              <Icon name="Columns2" size={14} /> Fields
            </button>
            <button className={s.secondaryBtn} onClick={() => exportTable('xlsx')} disabled={exporting || table.cell_count === 0}>
              <Icon name={exporting ? 'Loader2' : 'Download'} size={14} className={exporting ? s.spin : undefined} /> Excel
            </button>
            <button className={s.secondaryBtn} onClick={() => exportTable('csv')} disabled={exporting || table.cell_count === 0}>
              <Icon name="Download" size={14} /> CSV
            </button>
            {isOwner && (
              <button className={s.dangerBtn} onClick={deleteTable} disabled={running} title="Delete this review">
                <Icon name="Trash2" size={14} />
              </button>
            )}
          </div>
        </div>

        {schemaOpen && (
          <div className={s.panel}>
            <div className={s.panelHead}>
              <span className={s.panelTitle}>Fields to extract</span>
              <span className={s.panelHint}>Removing a field deletes its extracted column.</span>
            </div>
            <ReviewSchemaEditor fields={draftFields} onChange={setDraftFields} onAddStandardFields={addStandard} styles={s} />
            <div className={s.panelActions}>
              <button type="button" className={s.primaryBtn} onClick={submitSchema}><Icon name="Check" size={14} /> Save fields</button>
              <button type="button" className={s.secondaryBtn} onClick={() => setSchemaOpen(false)}>Cancel</button>
            </div>
          </div>
        )}

        <ReviewRunBar
          estimate={estimate}
          running={running}
          progress={progress}
          runError={runError}
          canRun={canRun}
          onRun={() => run()}
          styles={s}
        />

        {table.documents.length === 0 || table.fields.length === 0 ? (
          <div className={s.empty}>
            <Icon name="Table2" size={18} />
            <p>
              {table.documents.length === 0 && table.fields.length === 0
                ? 'Pick the documents to review and the fields to extract.'
                : table.documents.length === 0
                  ? 'Pick the documents to review.'
                  : 'Add at least one field to extract.'}
            </p>
          </div>
        ) : (
          <div className={`${s.workspace} ${selectedCell ? s.workspaceWithPopover : ''}`}>
            <ReviewResultsGrid
              fields={table.fields}
              documents={table.documents}
              cells={cells}
              selected={selectedCell}
              running={running}
              onSelect={selectCell}
              onRerunRow={(documentId) => run({ documentIds: [documentId] })}
              onRerunColumn={(fieldKey) => run({ fieldKeys: [fieldKey] })}
              styles={s}
            />
            {selectedCell && (
              <ReviewCellPopover
                cell={selected}
                field={selectedField}
                document={selectedDoc}
                running={running}
                onRerun={() => run({ documentIds: [selectedCell.documentId], fieldKeys: [selectedCell.fieldKey] })}
                onClose={() => selectCell(null)}
                styles={s}
              />
            )}
          </div>
        )}

        <ReviewDocumentPicker
          isOpen={pickerOpen}
          documents={pickerDocs}
          loading={pickerLoading}
          selectedIds={table.documents.map((d) => d.id)}
          scopeLabel={activeMatter
            ? `Documents filed under "${activeMatter.is_personal ? 'Personal' : activeMatter.name}"`
            : 'Every indexed document you can read. Set an active matter to narrow this list.'}
          onConfirm={saveDocuments}
          onClose={closePicker}
          styles={s}
        />
      </div>
    </div>
  );
}

export default ReviewTableView;
