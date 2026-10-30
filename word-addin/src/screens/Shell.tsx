import { useEffect } from 'react';
import { type Screen, useAddinStore } from '../store';
import { Analyze } from './Analyze';
import { Research } from './Research';
import { Settings } from './Settings';
import { SignIn } from './SignIn';
import s from '../styles/pane.module.css';

const TABS: { id: Screen; label: string }[] = [
  { id: 'analyze', label: 'Analyze' },
  { id: 'research', label: 'Research' },
  { id: 'settings', label: 'Settings' },
];

/** Header, tabs and the active screen; the sign-in card replaces the work screens when signed out. */
export function Shell() {
  const { booted, session, screen, setScreen, init } = useAddinStore();
  useEffect(() => {
    void init();
  }, [init]);

  const body = !booted ? (
    <p className={s.muted}>
      <span className={s.spinner} />
      Loading…
    </p>
  ) : screen === 'settings' ? (
    <Settings />
  ) : !session ? (
    <SignIn />
  ) : screen === 'research' ? (
    <Research />
  ) : (
    <Analyze />
  );

  return (
    <div className={s.pane}>
      <header className={s.header}>
        <span className={s.brand}>CaseCite<span className={s.brandByline}>by PairSys</span></span>
        <span className={s.who}>{session ? session.user.email : 'Not signed in'}</span>
      </header>
      <nav className={s.tabs} aria-label="Sections">
        {TABS.map((tab) => (
          <button
            key={tab.id}
            type="button"
            className={`${s.tab} ${screen === tab.id ? s.tabActive : ''}`}
            onClick={() => setScreen(tab.id)}
            aria-current={screen === tab.id ? 'page' : undefined}
          >
            {tab.label}
          </button>
        ))}
      </nav>
      <main className={s.content}>{body}</main>
    </div>
  );
}
