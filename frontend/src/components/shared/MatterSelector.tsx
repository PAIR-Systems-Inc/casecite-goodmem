import { useEffect } from 'react';
import { useMattersStore } from '../../stores/mattersStore';
import { appNavigate } from '../../utils/router';
import { Icon } from './Icon';
import s from './MatterSelector.module.css';

/**
 * Header control for the active matter. Research, Contracts and Drafting
 * default their document scope to it; "All matters" (the default) leaves
 * every view showing everything the user can see.
 */
export function MatterSelector() {
  const matters = useMattersStore((st) => st.matters);
  const activeMatterId = useMattersStore((st) => st.activeMatterId);
  const setActiveMatter = useMattersStore((st) => st.setActiveMatter);
  const init = useMattersStore((st) => st.init);
  useEffect(() => { init(); }, [init]);

  const active = matters.find((m) => m.id === activeMatterId) || null;

  return (
    <div className={s.wrap}>
      <label className={s.label} htmlFor="active-matter-select">
        <Icon name="Briefcase" size={14} className={s.icon} />
        <span className="hide-on-mobile">Matter</span>
      </label>
      <select
        id="active-matter-select"
        className={`${s.select} ${active ? s.selectActive : ''}`}
        value={activeMatterId || ''}
        onChange={(e) => setActiveMatter(e.target.value || null)}
        title={active ? `Scoped to ${active.name}` : 'Showing everything you can see'}
      >
        <option value="">All matters</option>
        {matters.map((m) => (
          <option key={m.id} value={m.id}>
            {m.is_personal ? 'Personal' : m.name}
            {m.client_name ? ` — ${m.client_name}` : ''}
          </option>
        ))}
      </select>
      <button
        type="button"
        className={s.manageBtn}
        onClick={() => appNavigate(active ? `/matters/${encodeURIComponent(active.id)}` : '/matters')}
        title={active ? 'Open this matter' : 'Manage matters'}
        aria-label={active ? 'Open this matter' : 'Manage matters'}
      >
        <Icon name={active ? 'ExternalLink' : 'FolderKanban'} size={14} />
      </button>
    </div>
  );
}
