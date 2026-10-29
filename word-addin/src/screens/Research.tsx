import { CitationList } from '../components/CitationList';
import { Notice } from '../components/Notice';
import { useAddinStore } from '../store';
import s from '../styles/pane.module.css';

/** Ask the firm's documents a question and insert the answer with footnotes. */
export function Research() {
  const { research, matters, matterId, setMatter, setQuestion, ask, insertAnswer } = useAddinStore();
  const running = research.status === 'running';

  return (
    <>
      <div className={s.card}>
        <h2 className={s.heading}>Research</h2>
        <div className={s.field}>
          <label className={s.label} htmlFor="matter">
            Search within
          </label>
          <select
            id="matter"
            className={s.select}
            value={matterId ?? ''}
            onChange={(e) => setMatter(e.target.value || null)}
            disabled={running}
          >
            <option value="">All my documents</option>
            {matters.map((m) => (
              <option key={m.id} value={m.id}>
                {m.is_personal ? 'Personal' : m.name}
                {m.client_name ? ` — ${m.client_name}` : ''}
              </option>
            ))}
          </select>
        </div>
        <div className={s.field}>
          <label className={s.label} htmlFor="question">
            Question
          </label>
          <textarea
            id="question"
            className={s.textarea}
            placeholder="e.g. What indemnity caps have we agreed with this counterparty before?"
            value={research.question}
            onChange={(e) => setQuestion(e.target.value)}
            disabled={running}
          />
        </div>
        <button type="button" className={s.primary} onClick={ask} disabled={running || !research.question.trim()}>
          {running ? <span className={s.spinner} /> : null}
          {running ? 'Researching…' : 'Ask'}
        </button>
        {research.status === 'error' && (
          <p className={s.error} role="alert">
            {research.error}
          </p>
        )}
      </div>

      {research.result && (
        <div className={s.card}>
          <h2 className={s.heading}>Answer</h2>
          <p className={s.answer}>{research.result.content}</p>
          <p className={s.label}>Sources</p>
          <CitationList citations={research.result.citations} />
          <div className={s.row}>
            <button type="button" className={s.primary} onClick={insertAnswer} disabled={research.inserting}>
              {research.inserting ? <span className={s.spinner} /> : null}
              Insert at cursor with footnotes
            </button>
          </div>
          {research.note && <p className={s.note}>{research.note}</p>}
          <Notice />
        </div>
      )}
    </>
  );
}
