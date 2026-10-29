import { useState } from 'react';
import { ArrowUpRight, X, Download } from 'lucide-react';
import { api, url, type Result } from './api';
import { type View, labels } from './constants';
import { Readable } from './ui';

export function ResultView({
  result: r,
  setResult,
  preview,
  report,
}: {
  result: Result;
  setResult: (r: Result | null) => void;
  preview: (id: string) => void;
  report: (e: unknown) => void;
}) {
  const [saving, setSaving] = useState(false);
  return (
    <section className="result card">
      <div className="result-toolbar">
        <span className="eyebrow">{labels[r.kind as View] || 'RESULT'}</span>
        <div>
          <a href={url(`/results/${r.id}/export?format=docx`)}>
            <Download size={14} />
            Word
          </a>
          <a href={url(`/results/${r.id}/export?format=md`)}>Markdown</a>
          {r.rows && <a href={url(`/results/${r.id}/export?format=csv`)}>CSV</a>}
          <button className="icon" title="Close result" aria-label="Close result" onClick={() => setResult(null)}>
            <X size={16} />
          </button>
        </div>
      </div>
      {r.warnings?.map((w, i) => (
        <p className="warning" key={i}>
          {w}
        </p>
      ))}
      {r.coverage?.some((c) => c.coverage !== 'full text') && (
        <p className="warning">
          This review uses retrieved excerpts for some documents. It cannot establish that a clause is absent from the
          full document.
        </p>
      )}
      <Readable text={r.answer} />
      {!!r.findings?.length && (
        <div className="findings">
          <h3>Findings & proposed language</h3>
          {r.findings.map((f, i) => (
            <article className="finding" key={i}>
              <div className="finding-title">
                <span className={`badge ${f.severity === 'critical' || f.severity === 'major' ? 'bad' : ''}`}>
                  {f.severity}
                </span>
                <h4>{f.title}</h4>
              </div>
              <p>{f.explanation}</p>
              {f.quote && (
                <>
                  <blockquote>{f.quote}</blockquote>
                  <div className="evidence-meta">
                    <span className={f.quote_verified ? 'verified' : 'unverified'}>
                      {f.quote_verified ? 'Exact quote matched' : 'Quote not verified'}
                    </span>
                    {f.document_id && (
                      <button onClick={() => preview(f.document_id!)}>
                        Open source
                        <ArrowUpRight size={13} />
                      </button>
                    )}
                  </div>
                </>
              )}
              {f.proposed_language && (
                <details>
                  <summary>Proposed language</summary>
                  <p>{f.proposed_language}</p>
                </details>
              )}
            </article>
          ))}
        </div>
      )}
      {!!r.rows?.length && (
        <div className="review-rows">
          <h3>Extracted terms</h3>
          {r.rows.map((row) => (
            <article key={row.document_id}>
              <h4>
                <button onClick={() => preview(row.document_id)}>
                  {row.filename}
                  <ArrowUpRight size={14} />
                </button>
              </h4>
              {row.cells.map((cell) => (
                <div className="review-cell" key={cell.field}>
                  <div>
                    <strong>{cell.field}</strong>
                    <p>{cell.value}</p>
                    {cell.quote && <blockquote>{cell.quote}</blockquote>}
                    <small className={cell.quote_verified ? 'verified' : 'unverified'}>
                      {cell.quote_verified ? 'Exact quote matched' : 'No verified quote'}
                    </small>
                  </div>
                  <label>
                    Review
                    <select
                      disabled={saving}
                      value={cell.review_status || 'pending'}
                      onChange={async (e) => {
                        setSaving(true);
                        try {
                          await api(`/results/${r.id}/cells`, 'PATCH', {
                            document_id: row.document_id,
                            field: cell.field,
                            review_status: e.target.value,
                          });
                          setResult(await api<Result>(`/results/${r.id}`));
                        } catch (e) {
                          report(e);
                        } finally {
                          setSaving(false);
                        }
                      }}
                    >
                      <option value="pending">Pending</option>
                      <option value="approved">Approved</option>
                      <option value="flagged">Flagged</option>
                    </select>
                  </label>
                </div>
              ))}
            </article>
          ))}
        </div>
      )}
      {!!r.citations?.length && (
        <div className="citations">
          <h3>
            Source passages <span>{r.citations.length}</span>
          </h3>
          {r.citations.map((c) => (
            <details className="citation" key={c.number} open={r.citations!.length === 1}>
              <summary>
                <span className="citation-number">{c.number}</span>
                <strong>{c.source}</strong>
                <small>{c.cited_in_answer ? 'Cited' : 'Retrieved'}</small>
              </summary>
              <blockquote>{c.passage}</blockquote>
              <div className="evidence-meta">
                <small>{c.verification_scope}</small>
                {c.document_id && (
                  <button onClick={() => preview(c.document_id!)}>
                    Open document
                    <ArrowUpRight size={13} />
                  </button>
                )}
                {c.url && (
                  <a href={c.url} target="_blank" rel="noreferrer">
                    Full opinion
                    <ArrowUpRight size={13} />
                  </a>
                )}
              </div>
            </details>
          ))}
        </div>
      )}
      {r.coverage && (
        <details className="coverage">
          <summary>Document coverage</summary>
          {r.coverage.map((c) => (
            <p key={c.document_id}>
              {c.filename}: {c.coverage}
            </p>
          ))}
        </details>
      )}
      <div className="result-foot">
        <span>Quote matching verifies text, not the interpretation.</span>
        {r.stats?.model && (
          <small>
            {r.stats.model}
            {r.stats.usage?.total_tokens ? ` · ${r.stats.usage.total_tokens.toLocaleString()} tokens` : ''}
          </small>
        )}
        <button
          className="text-danger"
          onClick={async () => {
            if (!confirm('Delete this saved result?')) return;
            try {
              await api(`/results/${r.id}`, 'DELETE');
              setResult(null);
            } catch (e) {
              report(e);
            }
          }}
        >
          Delete result
        </button>
      </div>
    </section>
  );
}
