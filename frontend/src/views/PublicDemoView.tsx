import { useEffect } from 'react';
import { usePublicDemoStore } from '../stores/publicDemoStore';
import styles from './PublicDemoView.module.css';

export default function PublicDemoView() {
  const state = usePublicDemoStore();
  useEffect(() => {
    // Refresh processing status and check private-session expiry while open.
    if (state.scope !== 'private' && !state.documents.some((d) => ['processing', 'uploading', 'upload_uncertain'].includes(d.status))) return;
    const timer = window.setInterval(() => { void state.refresh(); }, 15000);
    return () => window.clearInterval(timer);
  }, [state.scope, state.documents, state.refresh]);
  return <main className={styles.page}>
    <header><p className={styles.eyebrow}>PAIRED SYSTEMS · GOODMEM</p><h1>CaseCite</h1>
      <p>Ask questions about documents. Read the source passages behind each answer.</p></header>
    <nav aria-label="Document collection" className={styles.tabs}>
      <button aria-pressed={state.scope === 'public'} onClick={() => void state.select('public')}>Public documents</button>
      {state.session && <button aria-pressed={state.scope === 'private'} onClick={() => void state.select('private')}>My private documents</button>}
      {state.session && <button onClick={() => void state.logout()}>End private session</button>}
    </nav>
    <div className={styles.layout}>
      <aside className={styles.panel}>
        <h2>{state.scope === 'private' ? 'My private documents' : 'Public collection'}</h2>
        <p>{state.scope === 'private' ? 'These documents are separate from the public collection and other guests. The service operator and configured processing providers can process them.' : 'Anyone can query this collection. Uploads require a private invitation.'}</p>
        {state.scope === 'private' && state.session && <>
          <p>{state.session.uploads_used} of {state.session.upload_limit} uploads used.</p>
          <p>Access ends {new Date(state.session.expires_at).toLocaleString(undefined, { hour12: false })}.</p>
          <label className={styles.upload}>Upload a document
            <input type="file" disabled={state.busy || state.session.uploads_used >= state.session.upload_limit} onChange={(event) => {
              const file = event.target.files?.[0];
              if (file) void state.upload(file);
              event.target.value = '';
            }} />
          </label>
          <p>Up to 10 documents in total. A failed processing attempt can still use a slot. Ending this session requires a new invitation to return.</p>
        </>}
        <ul>{state.documents.map((doc) => <li key={doc.id}><strong>{doc.filename}</strong><span>{doc.status.replace(/_/g, ' ')}</span></li>)}</ul>
        {!state.documents.length && <p>No documents in this collection yet.</p>}
        <button onClick={() => void state.refresh()}>Refresh documents</button>
      </aside>
      <section className={styles.panel} aria-label="Document questions">
        <h2>Ask about this collection</h2>
        <p>Each question is independent. Answers can be wrong; check the cited passages. This demo does not provide legal advice.</p>
        {state.error && <p role="alert" className={styles.error}>{state.error}</p>}
        <div aria-live="polite">
          {state.messages.map((message, i) => <article className={styles.answer} key={i}>
            <h3>{message.question}</h3><p>{message.answer}</p>
            {message.sources.map((source) => <details key={source.number}><summary>[{source.number}] {source.filename}</summary><blockquote>{source.text}</blockquote></details>)}
          </article>)}
          {state.busy && <p role="status">Working…</p>}
        </div>
        <form onSubmit={(event) => { event.preventDefault(); void state.ask(); }}>
          <label htmlFor="demo-question">Your question</label>
          <textarea id="demo-question" value={state.question} onChange={(event) => state.setQuestion(event.target.value)} maxLength={2000} rows={3} placeholder="What are the main obligations in these documents?" />
          <button type="submit" disabled={state.busy || !state.question.trim() || !state.documents.some((d) => d.status === 'indexed')}>Ask CaseCite</button>
        </form>
      </section>
    </div>
  </main>;
}
