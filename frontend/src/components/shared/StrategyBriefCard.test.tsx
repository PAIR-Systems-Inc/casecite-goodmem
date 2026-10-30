import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/react';
import { StrategyBriefCard } from './StrategyBriefCard';
import type { StrategyBriefResponse } from '../../api/types';

const s = new Proxy({} as Record<string, string>, { get: (_, key) => String(key) });

function brief(scope: Partial<StrategyBriefResponse['scope']>): StrategyBriefResponse {
  return {
    question: 'Can we terminate?',
    scope: { documents_considered: 3, documents_total: 3, ...scope },
    position: 'Termination is defensible.',
    strengths: [],
    weaknesses: [],
    next_steps: [],
    citations: [],
  };
}

describe('StrategyBriefCard scope', () => {
  it('lists every folder the brief was scoped to', () => {
    const { container } = render(
      <StrategyBriefCard
        brief={brief({ folder_path: 'Acme/Leases', folder_paths: ['Acme/Leases', 'Acme/Correspondence'] })}
        citations={[]}
        onCitationClick={vi.fn()}
        styles={s}
      />,
    );
    expect(container.textContent).toContain('2 folders: Acme/Leases, Acme/Correspondence');
  });

  it('falls back to the legacy single folder field', () => {
    const { container } = render(
      <StrategyBriefCard brief={brief({ folder_path: 'Acme/Leases' })} citations={[]} onCitationClick={vi.fn()} styles={s} />,
    );
    expect(container.textContent).toContain('· in Acme/Leases');
    expect(container.textContent).not.toContain('folders:');
  });

  it('says nothing about folders for an unscoped brief', () => {
    const { container } = render(
      <StrategyBriefCard brief={brief({})} citations={[]} onCitationClick={vi.fn()} styles={s} />,
    );
    expect(container.textContent).not.toContain('· in');
  });

  it('counts files actually read and names missing documents', () => {
    const data = brief({ documents_read: 1, missing_documents: [{ document_id: 'b', filename: 'unreadable.pdf' }] });
    data.next_steps = [{ step: 'Confirm service.', citation_ids: [], support: 'unsupported' }];
    const { container } = render(<StrategyBriefCard brief={data} citations={[]} onCitationClick={vi.fn()} styles={s} />);
    expect(container.textContent).toContain('1 document reviewed (of 3)');
    expect(container.textContent).toContain('Could not read: unreadable.pdf');
    expect(container.textContent).toContain('No supporting document passage was identified for this point.');
  });

  it('does not invent the reason a supporting opinion was reviewed in part', () => {
    const data = brief({});
    data.case_law = { requested: true, points_searched: 2, opinions_read: 3, partially_read: 1, attached: 1, timed_out: true };
    const { container } = render(<StrategyBriefCard brief={data} citations={[]} onCitationClick={vi.fn()} styles={s} />);
    expect(container.textContent).toContain('1 opinion review covered only part of the text');
    expect(container.textContent).not.toContain('too long');
    expect(container.textContent).toContain('some points may remain unchecked');
  });
});
