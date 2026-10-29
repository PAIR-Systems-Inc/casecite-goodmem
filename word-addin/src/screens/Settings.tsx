import { useEffect, useState } from 'react';
import { useAddinStore } from '../store';
import s from '../styles/pane.module.css';

/** Server address, the matter the pane works in, and sign-out. */
export function Settings() {
  const { serverUrl, session, matters, matterId, mattersError, setServerUrl, setMatter, signOut, setScreen } =
    useAddinStore();
  const [draft, setDraft] = useState(serverUrl);
  useEffect(() => setDraft(serverUrl), [serverUrl]);
  const dirty = draft.trim().replace(/\/+$/, '') !== serverUrl;

  return (
    <>
      <div className={s.card}>
        <h2 className={s.heading}>CaseCite server</h2>
        <p className={s.text}>The address you open CaseCite at in your browser, for example https://casecite.yourfirm.com.</p>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void setServerUrl(draft);
          }}
        >
          <div className={s.field}>
            <label className={s.label} htmlFor="server">
              Server address
            </label>
            <input
              id="server"
              className={s.input}
              type="url"
              placeholder="https://casecite.yourfirm.com"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              required
            />
          </div>
          <div className={s.row}>
            <button type="submit" className={s.primary} disabled={!dirty || !draft.trim()}>
              Save
            </button>
            {session && dirty && <span className={s.muted}>Changing the server signs you out.</span>}
          </div>
        </form>
      </div>

      <div className={s.card}>
        <h2 className={s.heading}>Account</h2>
        {session ? (
          <>
            <p className={s.text}>
              Signed in as <strong>{session.user.email}</strong>. This session is listed in CaseCite under
              Settings → Security → Sessions as “Word add-in”.
            </p>
            <div className={s.field}>
              <label className={s.label} htmlFor="default-matter">
                Matter
              </label>
              <select
                id="default-matter"
                className={s.select}
                value={matterId ?? ''}
                onChange={(e) => setMatter(e.target.value || null)}
              >
                <option value="">All my documents</option>
                {matters.map((m) => (
                  <option key={m.id} value={m.id}>
                    {m.is_personal ? 'Personal' : m.name}
                  </option>
                ))}
              </select>
              {mattersError && <p className={s.error}>{mattersError}</p>}
            </div>
            <button type="button" className={s.secondary} onClick={signOut}>
              Sign out
            </button>
          </>
        ) : (
          <>
            <p className={s.text}>Not signed in.</p>
            <button type="button" className={s.primary} onClick={() => setScreen('analyze')} disabled={!serverUrl}>
              Sign in
            </button>
          </>
        )}
      </div>
    </>
  );
}
