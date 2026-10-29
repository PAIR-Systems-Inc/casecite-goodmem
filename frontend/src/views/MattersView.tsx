import { useEffect, useState } from 'react';
import { useMattersStore } from '../stores/mattersStore';
import { Icon, MatterCard } from '../components';
import s from './MattersView.module.css';

/** The firm's matters: open a workspace, create one, or pick the active scope. */
function MattersView() {
  const { matters, mattersLoading, activeMatterId, init, loadMatters, createMatter, openMatter, setActiveMatter } =
    useMattersStore();
  useEffect(() => { init(); }, [init]);

  // Local UI state only: the create form's fields and visibility.
  const [formOpen, setFormOpen] = useState(false);
  const [name, setName] = useState('');
  const [client, setClient] = useState('');
  const [creating, setCreating] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setCreating(true);
    const matter = await createMatter(name, client);
    setCreating(false);
    if (matter) {
      setName(''); setClient(''); setFormOpen(false);
      openMatter(matter.id);
    }
  };

  const shared = matters.filter((m) => !m.is_personal);
  const personal = matters.find((m) => m.is_personal);

  return (
    <div className={s.container}>
      <div className={s.content}>
        <div className={s.headerRow}>
          <div>
            <h2 className={s.title}>Matters</h2>
            <p className={s.subtitle}>
              A matter groups the documents, research chats and contract analyses for one client
              engagement, and decides which colleagues can see them.
            </p>
          </div>
          <div className={s.headerActions}>
            <button className={s.secondaryBtn} onClick={loadMatters} disabled={mattersLoading}>
              <Icon name={mattersLoading ? 'Loader2' : 'RefreshCw'} size={14} className={mattersLoading ? s.spin : undefined} />
              Refresh
            </button>
            <button className={s.primaryBtn} onClick={() => setFormOpen((o) => !o)}>
              <Icon name="Plus" size={14} /> New matter
            </button>
          </div>
        </div>

        {formOpen && (
          <form className={s.createForm} onSubmit={submit}>
            <input
              className={s.input}
              placeholder="Matter name, e.g. Acme v. Beta"
              value={name}
              onChange={(e) => setName(e.target.value)}
              aria-label="Matter name"
              autoFocus
            />
            <input
              className={s.input}
              placeholder="Client (optional)"
              value={client}
              onChange={(e) => setClient(e.target.value)}
              aria-label="Client name"
            />
            <button type="submit" className={s.primaryBtn} disabled={creating || !name.trim()}>
              <Icon name={creating ? 'Loader2' : 'Check'} size={14} className={creating ? s.spin : undefined} />
              Create
            </button>
            <button type="button" className={s.secondaryBtn} onClick={() => setFormOpen(false)}>Cancel</button>
          </form>
        )}

        {activeMatterId && (
          <div className={s.scopeNote}>
            <Icon name="Target" size={14} />
            Research, Contracts and Drafting are scoped to the active matter.
            <button type="button" className={s.linkBtn} onClick={() => setActiveMatter(null)}>Show everything</button>
          </div>
        )}

        {mattersLoading && matters.length === 0 ? (
          <div className={s.empty}><Icon name="Loader2" size={16} className={s.spin} /> Loading matters…</div>
        ) : (
          <>
            <div className={s.sectionLabel}>Shared matters</div>
            {shared.length === 0 ? (
              <div className={s.empty}>
                <Icon name="Briefcase" size={18} />
                <p>No shared matters yet. Create one to work on a client engagement with colleagues.</p>
              </div>
            ) : (
              <div className={s.grid}>
                {shared.map((m) => (
                  <MatterCard
                    key={m.id}
                    matter={m}
                    isActive={m.id === activeMatterId}
                    onOpen={openMatter}
                    onSetActive={setActiveMatter}
                    styles={s}
                  />
                ))}
              </div>
            )}
            {personal && (
              <>
                <div className={s.sectionLabel}>Private</div>
                <div className={s.grid}>
                  <MatterCard
                    matter={personal}
                    isActive={personal.id === activeMatterId}
                    onOpen={openMatter}
                    onSetActive={setActiveMatter}
                    styles={s}
                  />
                </div>
              </>
            )}
          </>
        )}
      </div>
    </div>
  );
}

export default MattersView;
