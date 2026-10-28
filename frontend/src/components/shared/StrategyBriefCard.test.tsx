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
        s={s}
      />,
    );
    expect(container.textContent).toContain('2 folders: Acme/Leases, Acme/Correspondence');
  });

  it('falls back to the legacy single folder field', () => {
    const { container } = render(
      <StrategyBriefCard brief={brief({ folder_path: 'Acme/Leases' })} citations={[]} onCitationClick={vi.fn()} s={s} />,
    );
    expect(container.textContent).toContain('· in Acme/Leases');
    expect(container.textContent).not.toContain('folders:');
  });

  it('says nothing about folders for an unscoped brief', () => {
    const { container } = render(
      <StrategyBriefCard brief={brief({})} citations={[]} onCitationClick={vi.fn()} s={s} />,
    );
    expect(container.textContent).not.toContain('· in');
  });
});
