import { lazy, Suspense, useEffect } from 'react';
import { usePublicDemoStore } from './stores/publicDemoStore';

const FullApp = lazy(() => import('./App').then(({ App }) => ({ default: App })));
const Demo = lazy(() => import('./views/PublicDemoView'));

export default function PublicDemoEntry() {
  const { mode, error, initialize } = usePublicDemoStore();
  useEffect(() => { void initialize(); }, [initialize]);
  if (mode === 'loading') return <p role="status">Loading CaseCite…</p>;
  if (mode === 'error') return <p role="alert">CaseCite could not load. {error} Reload this page to retry.</p>;
  return <Suspense fallback={<p role="status">Loading CaseCite…</p>}>
    {mode === 'demo' ? <Demo /> : <FullApp />}
  </Suspense>;
}
