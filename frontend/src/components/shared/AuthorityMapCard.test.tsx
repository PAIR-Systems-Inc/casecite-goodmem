import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { AuthorityMapRecordCard } from './AuthorityMapCard';
import type { ChatAuthorityMapRecord } from '../../api/types';

const completed: ChatAuthorityMapRecord = {
  job_id: 'job-1',
  documents: [{ id: 'doc-1', name: 'Motion.pdf' }, { id: 'doc-2', name: 'Reply.pdf' }],
  status: 'completed',
  runs: [
    { document_id: 'doc-1', document_name: 'Motion.pdf', run_id: 'run-1', summary: { verified: 2, authorities: 3, propositions: 4 } },
    { document_id: 'doc-2', document_name: 'Reply.pdf', run_id: null, summary: { verified: 0, authorities: 0, propositions: 1 } },
  ],
  totals: { files: 2, propositions: 5, authorities: 3, verified: 2 },
  coverage_notes: ['Reply.pdf was read only in part.'],
};

describe('AuthorityMapRecordCard', () => {
  it('shows one line per run and opens the stored run by id', () => {
    const onOpenRun = vi.fn();
    render(<AuthorityMapRecordCard record={completed} onOpenRun={onOpenRun} />);

    expect(screen.getByText('2 verified · 3 authorities · 5 propositions')).toBeInTheDocument();
    expect(screen.getByText('Motion.pdf')).toBeInTheDocument();
    expect(screen.getByText('2 verified · 3 authorities · 4 propositions')).toBeInTheDocument();
    expect(screen.getByText('Reply.pdf was read only in part.')).toBeInTheDocument();

    fireEvent.click(screen.getByText('Motion.pdf'));
    expect(onOpenRun).toHaveBeenCalledWith('run-1', completed.runs![0]);

    // A file without a stored run has nothing to open.
    const replyRow = screen.getByText('Reply.pdf').closest('button')!;
    expect(replyRow).toBeDisabled();
  });

  it('states a running or failed job instead of inventing results', () => {
    const onOpenRun = vi.fn();
    const { rerender } = render(
      <AuthorityMapRecordCard record={{ ...completed, status: 'running', runs: undefined, totals: undefined }} onOpenRun={onOpenRun} />,
    );
    expect(screen.getByText(/Still running/)).toBeInTheDocument();
    expect(screen.getByText('Motion.pdf, Reply.pdf')).toBeInTheDocument();

    rerender(
      <AuthorityMapRecordCard record={{ ...completed, status: 'failed', runs: undefined, totals: undefined, error: 'TimeoutError' }} onOpenRun={onOpenRun} />,
    );
    expect(screen.getByText('Failed (TimeoutError)')).toBeInTheDocument();
    expect(onOpenRun).not.toHaveBeenCalled();
  });
});
