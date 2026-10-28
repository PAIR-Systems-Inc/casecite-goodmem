import React from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';

/**
 * Office calls `Office.onReady` once the host is ready; rendering before that
 * can run Word.run before the bridge exists. Outside Office (a plain browser
 * tab during development) there is no Office global, so render at once; the
 * document actions then fail with a clear message instead of a blank pane.
 */
function render() {
  const root = document.getElementById('root');
  if (!root) return;
  createRoot(root).render(
    <React.StrictMode>
      <App />
    </React.StrictMode>,
  );
}

const office = (globalThis as { Office?: typeof Office }).Office;
if (office?.onReady) {
  office.onReady(() => render());
} else {
  render();
}
