import { useEffect, useState } from 'react';
import { useReviewTablesStore, STANDARD_CONTRACT_FIELDS } from '../stores/reviewTablesStore';
import { useMattersStore } from '../stores/mattersStore';
import { Icon, ReviewTableCard, ReviewSchemaEditor } from '../components';
import type { ReviewFieldSpec } from '../api/types';
import s from './ReviewTablesView.module.css';

/** The firm's review tables: open one, or define a new schema and sweep documents with it. */
function ReviewTablesView() {
  const { tables, tablesLoading, init, loadTables, createTable, openTable } = useReviewTablesStore();
  const activeMatter = useMattersStore((st) => st.activeMatter());
  const mattersInit = useMattersStore((st) => st.init);
  useEffect(() => { init(); mattersInit(); }, [init, mattersInit]);

  // Local UI state only: the create form.
  const [formOpen, setFormOpen] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [fields, setFields] = useState<ReviewFieldSpec[]>(STANDARD_CONTRACT_FIELDS);
  const [creating, setCreating] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setCreating(true);
    const table = await createTable({ name, description, fields, documentIds: [] });
    setCreating(false);
    if (table) {
      setName(''); setDescription(''); setFields(STANDARD_CONTRACT_FIELDS); setFormOpen(false);
      openTable(table.id);
    }
  };

  const addStandard = () =>
    setFields((prev) => [...prev, ...STANDARD_CONTRACT_FIELDS.filter((f) => !prev.some((p) => p.name.toLowerCase() === f.name.toLowerCase()))]);

  return (
    <div className={s.container}>
      <div className={s.content}>
        <div className={s.headerRow}>
          <div>
            <h2 className={s.title}>Tabular Review</h2>
            <p className={s.subtitle}>
              Define the facts you need, point the table at a set of contracts, and get one row per
              document with every value tied to the exact passage that supports it. Export to Excel
              with the citations alongside.
            </p>
          </div>
          <div className={s.headerActions}>
            <button className={s.secondaryBtn} onClick={loadTables} disabled={tablesLoading}>
              <Icon name={tablesLoading ? 'Loader2' : 'RefreshCw'} size={14} className={tablesLoading ? s.spin : undefined} />
              Refresh
            </button>
            <button className={s.primaryBtn} onClick={() => setFormOpen((o) => !o)}>
              <Icon name="Plus" size={14} /> New review
            </button>
          </div>
        </div>

        {formOpen && (
          <form className={s.createForm} onSubmit={submit}>
            <div className={s.formRow}>
              <input
                className={s.input}
                placeholder="Review name, e.g. Q3 vendor NDA sweep"
                value={name}
                onChange={(e) => setName(e.target.value)}
                aria-label="Review name"
                autoFocus
              />
              <input
                className={`${s.input} ${s.inputWide}`}
                placeholder="What this review is for (optional)"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                aria-label="Description"
              />
            </div>
            <div className={s.formLabel}>Fields to extract</div>
            <ReviewSchemaEditor fields={fields} onChange={setFields} onAddStandardFields={addStandard} styles={s} />
            <div className={s.formNote}>
              <Icon name={activeMatter ? 'Target' : 'Info'} size={13} />
              {activeMatter
                ? `Scoped to every document filed under "${activeMatter.is_personal ? 'Personal' : activeMatter.name}". You can change the documents after creating it.`
                : 'You pick the documents on the next screen. Set an active matter in the header to start from everything filed under it.'}
            </div>
            <div className={s.formActions}>
              <button type="submit" className={s.primaryBtn} disabled={creating || !name.trim()}>
                <Icon name={creating ? 'Loader2' : 'Check'} size={14} className={creating ? s.spin : undefined} />
                Create review
              </button>
              <button type="button" className={s.secondaryBtn} onClick={() => setFormOpen(false)}>Cancel</button>
            </div>
          </form>
        )}

        {tablesLoading && tables.length === 0 ? (
          <div className={s.empty}><Icon name="Loader2" size={16} className={s.spin} /> Loading review tables…</div>
        ) : tables.length === 0 ? (
          <div className={s.empty}>
            <Icon name="Table2" size={18} />
            <p>No review tables yet. Create one to extract the same fields from many documents at once.</p>
          </div>
        ) : (
          <div className={s.grid}>
            {tables.map((t) => (
              <ReviewTableCard key={t.id} table={t} onOpen={openTable} styles={s} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

export default ReviewTablesView;
