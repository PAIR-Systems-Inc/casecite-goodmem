import type { ContractKeyTerm } from '../api/types';
import s from '../styles/pane.module.css';

interface KeyTermsTableProps {
  terms: Record<string, ContractKeyTerm>;
  onSelect: (term: ContractKeyTerm) => void;
}

function label(field: string): string {
  return field.replace(/_/g, ' ').replace(/^\w/, (c) => c.toUpperCase());
}

/** The key-terms table; a verified term can be selected in the document. */
export function KeyTermsTable({ terms, onSelect }: KeyTermsTableProps) {
  const entries = Object.entries(terms);
  if (entries.length === 0) return <p className={s.muted}>No key terms were extracted.</p>;
  return (
    <table className={s.table}>
      <thead>
        <tr>
          <th>Term</th>
          <th>Value</th>
          <th aria-label="Actions" />
        </tr>
      </thead>
      <tbody>
        {entries.map(([field, term]) => (
          <tr key={field}>
            <td>{label(field)}</td>
            <td>
              {term.value}
              {term.verified && <span className={`${s.badge} ${s.badgeVerified}`}>verified</span>}
            </td>
            <td>
              {term.quote ? (
                <button type="button" className={s.link} onClick={() => onSelect(term)}>
                  Select
                </button>
              ) : null}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
