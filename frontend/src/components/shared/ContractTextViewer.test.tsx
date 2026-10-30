import { render, screen } from '@testing-library/react';
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';
import { ContractTextViewer } from './ContractTextViewer';

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
});
