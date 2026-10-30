import { describe, it, expect, vi } from 'vitest';
import { citationLine, replaceQuoteTracked, selectQuote } from './document';

/**
 * A minimal Word.run stand-in: `body.search` answers from a table of search
 * strings to hit counts, and the ranges record select / insertText calls.
 */
function installWord(hits: Record<string, number>) {
  const selected: string[] = [];
  const inserted: Array<{ text: string; mode: string }> = [];
  const trackingChanges: string[] = [];
  const makeRange = (term: string) => ({
    text: term,
    load: vi.fn(),
    select: vi.fn((mode: string) => selected.push(`${term}|${mode}`)),
    insertText: vi.fn((text: string, mode: string) => {
      inserted.push({ text, mode });
      return { select: vi.fn() };
    }),
  });
  const document = {
    changeTrackingMode: 'Off',
    load: vi.fn(),
    getSelection: () => makeRange(''),
    body: {
      search: vi.fn((term: string) => {
        const count = hits[term] ?? 0;
        return {
          items: Array.from({ length: count }, () => makeRange(term)),
          load: vi.fn(),
        };
      }),
    },
  };
  const context = { document, sync: vi.fn(async () => {}) };
  // Record every tracking-mode assignment so the test can see the restore.
  Object.defineProperty(document, 'changeTrackingMode', {
    get: () => trackingChanges[trackingChanges.length - 1] ?? 'Off',
    set: (v: string) => {
      trackingChanges.push(v);
    },
  });
  (globalThis as Record<string, unknown>).Word = {
    run: vi.fn(async (batch: (ctx: unknown) => Promise<unknown>) => batch(context)),
    ChangeTrackingMode: { trackAll: 'TrackAll', off: 'Off' },
  };
  return { document, selected, inserted, trackingChanges };
}

describe('selectQuote', () => {
  it('selects the first search string that Word finds', async () => {
    const quote = 'The Supplier shall indemnify the Customer. This survives termination.';
    const { selected } = installWord({ [quote]: 1 });
    const result = await selectQuote(quote);
    expect(result).toEqual({ found: true, matched: quote });
    expect(selected).toEqual([`${quote}|Select`]);
  });

  it('falls back to the first sentence when the whole quote is not found', async () => {
    const quote = 'The Supplier shall indemnify the Customer. This survives termination.';
    const { document, selected } = installWord({ 'The Supplier shall indemnify the Customer.': 2 });
    const result = await selectQuote(quote);
    expect(result.found).toBe(true);
    expect(result.matched).toBe('The Supplier shall indemnify the Customer.');
    expect(document.body.search).toHaveBeenCalledTimes(2);
    expect(selected).toHaveLength(1);
  });

  it('reports not found after every candidate misses', async () => {
    installWord({});
    expect(await selectQuote('Nothing in this document says this at all.')).toEqual({ found: false });
    expect(await selectQuote(null)).toEqual({ found: false });
  });
});

describe('replaceQuoteTracked', () => {
  it('turns tracking on for the replacement and restores the previous mode', async () => {
    const quote = 'Liability is unlimited.';
    const { inserted, trackingChanges } = installWord({ [quote]: 1 });
    const result = await replaceQuoteTracked(quote, 'Liability is capped at the fees paid.');
    expect(result.found).toBe(true);
    expect(inserted).toEqual([{ text: 'Liability is capped at the fees paid.', mode: 'Replace' }]);
    expect(trackingChanges).toEqual(['TrackAll', 'Off']);
  });

  it('changes nothing when the passage is not found', async () => {
    const { inserted, trackingChanges } = installWord({});
    const result = await replaceQuoteTracked('Missing passage from the contract.', 'New text');
    expect(result).toEqual({ found: false });
    expect(inserted).toEqual([]);
    expect(trackingChanges).toEqual([]);
  });
});

describe('citationLine', () => {
  it('joins source, a trimmed excerpt and the url', () => {
    const line = citationLine({
      id: 'c1',
      source: 'Smith v Jones',
      type: 'case_law',
      passage: `  ${'word '.repeat(80)}`,
      status: 'verified',
      url: 'https://www.courtlistener.com/x',
    });
    expect(line.startsWith('Smith v Jones — “')).toBe(true);
    expect(line.endsWith(' — https://www.courtlistener.com/x')).toBe(true);
    expect(line).toContain('…');
  });
});
