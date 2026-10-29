import type { Citation } from '../api/types';
import s from '../styles/pane.module.css';

interface CitationListProps {
  citations: Citation[];
}

function excerpt(passage: string): string {
  const clean = (passage || '').replace(/\s+/g, ' ').trim();
  return clean.length > 160 ? `${clean.slice(0, 160).trimEnd()}…` : clean;
}

/** The sources behind a research answer, numbered as the footnotes will be. */
export function CitationList({ citations }: CitationListProps) {
  if (citations.length === 0) return <p className={s.muted}>No sources were cited.</p>;
  return (
    <ol className={s.sources}>
      {citations.map((citation) => (
        <li key={citation.id}>
          {citation.url ? (
            <a href={citation.url} target="_blank" rel="noopener noreferrer">
              {citation.source}
            </a>
          ) : (
            citation.source
          )}
          {citation.passage ? ` — “${excerpt(citation.passage)}”` : ''}
        </li>
      ))}
    </ol>
  );
}
