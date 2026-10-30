import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';
import { ContractTextViewer } from './ContractTextViewer';
import type { ContractRedlineEdit } from '../../api/types';

describe('contract paragraph presentation', () => {
	const scrollIntoView = HTMLElement.prototype.scrollIntoView;
	beforeAll(() => { HTMLElement.prototype.scrollIntoView = vi.fn(); });
	afterAll(() => { HTMLElement.prototype.scrollIntoView = scrollIntoView; });
  const text = 'Payment is due in thirty days.\nConfirm receipt in writing.\n';
  const props = { text, loading: false, highlights: [], onClose: vi.fn(), onSelectionAction: vi.fn() };

  it('preserves canonical Word paragraph boundaries without changing quote text', () => {
    render(<ContractTextViewer {...props} filename="agreement.docx" highlights={['Confirm receipt in writing.']} />);
    const first = screen.getByText('Payment is due in thirty days.');
    const second = screen.getByText('Confirm receipt in writing.');
    expect(first.closest('p')).not.toBe(second.closest('p'));
    expect(second.closest('mark')).not.toBeNull();
  });

  it('reflows hard-wrapped PDF lines in a single paragraph', () => {
    const { container } = render(<ContractTextViewer {...props} filename="agreement.pdf" />);
    const paragraphs = container.querySelectorAll('p');
    expect(paragraphs).toHaveLength(1);
    expect(paragraphs[0].textContent).toBe(text);
  });

  it('ends normalized quote highlighting at the exact source boundary', () => {
    const { container } = render(<ContractTextViewer {...props} filename="agreement.pdf"
      text={'Before. Payment is\n  due in thirty days. Do not highlight this.'}
      highlights={['Payment is due in thirty days.']} />);
    expect(container.querySelector('mark')?.textContent).toBe('Payment is\n  due in thirty days.');
  });

  const table = '| Term | Value |\n| --- | --- |\n| Timing | 30 days |\n| | 60 days |';
  const tableText = `Before the table.\n\n${table}\n\nAfter the table.`;
  const edit = (original: string, proposed: string): ContractRedlineEdit => ({
    ref: 'r1', kind: 'replace', span_start: tableText.indexOf(original), span_end: tableText.indexOf(original) + original.length,
    original_text: original, proposed_text: proposed, title: 'Timing', rationale: 'Change the term', severity: 'minor',
  });

  it('renders structured cells without changing the editable source or quote marks', () => {
    render(<ContractTextViewer {...props} filename="agreement.pdf" text={tableText}
      highlights={['30 days']} onTextEdit={vi.fn()} />);
    const grid = screen.getByRole('table');
    expect(within(grid).getAllByRole('columnheader').map(el => el.textContent)).toEqual(['Term', 'Value']);
    expect(within(grid).getAllByRole('cell').map(el => el.textContent)).toEqual(['Timing', '30 days', '', '60 days']);
    expect(within(grid).getByText('30 days').closest('mark')).not.toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Edit text' }));
    expect(screen.getByRole('textbox', { name: 'Edit contract text' })).toHaveValue(tableText);
  });

  it('preserves a single-cell redline and its proposed wording in the grid', () => {
    const { rerender } = render(<ContractTextViewer {...props} filename="agreement.pdf" text={tableText} redlines={[edit('30 days', '45 days')]} />);
    expect(screen.getByText('30 days').closest('td')).not.toBeNull();
    expect(screen.getByText('30 days').closest('mark')).not.toBeNull();
    rerender(<ContractTextViewer {...props} filename="agreement.pdf" text={tableText} redlines={[edit('30 days', '45 days')]} redlineDecisions={{ r1: 'accepted' }} />);
    expect(screen.getByText('45 days').closest('ins')).not.toBeNull();
    expect(screen.getByText('45 days').closest('td')).not.toBeNull();
    expect(screen.getByText('60 days')).toBeInTheDocument();
  });

  it('keeps a cross-cell edit intact instead of splitting its replacement', () => {
    const original = 'Timing | 30 days';
    const { container, rerender } = render(<ContractTextViewer {...props} filename="agreement.pdf" text={tableText} redlines={[edit(original, 'New schedule')]} />);
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
    expect(container.querySelector('mark')?.textContent).toBe(original);
    rerender(<ContractTextViewer {...props} filename="agreement.pdf" text={tableText} redlines={[edit(original, 'New schedule')]} redlineDecisions={{ r1: 'accepted' }} />);
    expect(container.querySelectorAll('ins')).toHaveLength(1);
    expect(screen.getByText('New schedule')).toBeInTheDocument();
  });

  it('shows literal pipes, backslashes and HTML as cell text', () => {
    render(<ContractTextViewer {...props} filename="agreement.pdf" text={'| Value |\n| --- |\n| A\\|B and C\\\\D <script>alert(1)</script> |'} highlights={['|B']} />);
    const cell = screen.getByRole('cell');
    expect(cell.textContent).toBe('A|B and C\\D <script>alert(1)</script>');
    expect(cell.querySelector('mark')?.textContent).toBe('|B');
    expect(cell.querySelector('script')).toBeNull();
  });

  it('maps a selection across escaped table cells back to the canonical source', () => {
    const onSelectionAction = vi.fn();
    const original = '| Value | Other |\n| --- | --- |\n| A\\|B contract | End clause |';
    render(<ContractTextViewer {...props} filename="agreement.pdf" text={original} onSelectionAction={onSelectionAction} />);
    const cells = screen.getAllByRole('cell');
    const range = document.createRange();
    range.setStart(cells[0].firstChild!.firstChild!, 0);
    range.setEnd(cells[1].firstChild!.firstChild!, 10);
    const selection = window.getSelection()!;
    selection.removeAllRanges(); selection.addRange(range);
    range.getBoundingClientRect = () => ({ top: 10, left: 10, width: 100 } as DOMRect);
    fireEvent.mouseUp(cells[1]);
    fireEvent.click(screen.getByRole('button', { name: /^Explain$/ }));
    expect(onSelectionAction).toHaveBeenCalledWith('explain', 'A\\|B contract | End clause');
  });

  it('does not misinterpret ordinary pipe text or malformed table rows', () => {
    render(<ContractTextViewer {...props} filename="agreement.pdf" text={'A | B\n--- | ---\nC | D\n\n| Not a table |\n| no divider |'} />);
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });
});
