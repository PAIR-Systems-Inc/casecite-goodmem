import { describe, expect, it, vi } from 'vitest';
import { replaceQuoteTracked } from './document';
import { literalQuery, normalizedQuote, quoteEdge } from './quote-range';

// A position-based host double: searches return actual spans in the source,
// unions contain all intervening text, and insertion replaces that exact span.
// This checks the algorithm; it does not substitute for Microsoft Word QA.
function wordHost(source: string, options: { selection?: [number, number]; previous?: string; sync?: (count: number) => void } = {}) {
  let value = source;
  let selection = options.selection ?? [0, 0];
  let tracking = options.previous ?? 'Off';
  let syncCount = 0;
  let active = 0;
  let maxActive = 0;
  const trackingChanges: string[] = [];
  const changes: Array<{ before: string; after: string; tracking: string }> = [];
  const pending: Array<() => void> = [];
  const range = (start: number, end: number): Word.Range => ({
    get text() { return value.slice(start, end); },
    isNullObject: false,
    load: vi.fn(),
    select: vi.fn(() => { selection = [start, end]; }),
    compareLocationWith: (other: Word.Range & { start: number; end: number }) => ({
      value: start === other.start && end === other.end ? 'Equal' : end <= other.start ? 'Before' : start >= other.end ? 'After' : start < other.start ? 'OverlapsBefore' : 'OverlapsAfter',
    }),
    expandToOrNullObject: (other: Word.Range & { start: number; end: number }) => range(Math.min(start, other.start), Math.max(end, other.end)),
    insertText: (text: string) => {
      pending.push(() => {
        changes.push({ before: value.slice(start, end), after: text, tracking });
        value = value.slice(0, start) + text + value.slice(end);
      });
      return range(start, start + text.length);
    },
    start, end,
  } as unknown as Word.Range);
  const search = vi.fn((query: string) => {
    const literal = query.replace(/\^\^/g, '^').replace(/\s/g, '');
    let compact = '';
    const offsets: number[] = [];
    for (let i = 0; i < value.length; i++) {
      if (/\s/.test(value[i])) continue;
      compact += value[i]; offsets.push(i);
    }
    const items: Word.Range[] = [];
    if (literal) for (let from = 0; from < compact.length;) {
      const found = compact.indexOf(literal, from);
      if (found === -1) break;
      items.push(range(offsets[found], offsets[found + literal.length - 1] + 1));
      from = found + literal.length;
    }
    return { items, load: vi.fn() };
  });
  const document = {
    body: { search },
    getSelection: () => range(selection[0], selection[1]),
    load: vi.fn(),
    get changeTrackingMode() { return tracking; },
    set changeTrackingMode(mode: string) { trackingChanges.push(mode); tracking = mode; },
  };
  const context = { document, sync: vi.fn(async () => {
    try { options.sync?.(++syncCount); } catch (error) { pending.length = 0; throw error; }
    while (pending.length) pending.shift()!();
  }) };
  (globalThis as Record<string, unknown>).Word = {
    run: async (fn: (context: unknown) => Promise<unknown>) => {
      active++; maxActive = Math.max(maxActive, active);
      try { return await fn(context); } finally { active--; }
    },
    ChangeTrackingMode: { trackAll: 'TrackAll' },
  };
  return { search, changes, trackingChanges, context, text: () => value, maxActive: () => maxActive, change: (text: string) => { value = text; } };
}

describe('whole-passage tracked replacements', () => {
  const longQuote = `The parties agree to the following notice procedure. ${'A written notice must identify the breach and allow a reasonable cure period. '.repeat(8)}This obligation survives termination.`;

  it('replaces the complete long clause rather than only the first search fragment', async () => {
    const host = wordHost(`Header. ${longQuote} Footer.`);
    expect((await replaceQuoteTracked(longQuote, 'Replacement clause.')).found).toBe(true);
    expect(host.changes).toEqual([{ before: longQuote, after: 'Replacement clause.', tracking: 'TrackAll' }]);
    expect(host.text()).toBe('Header. Replacement clause. Footer.');
    expect(host.trackingChanges).toEqual(['TrackAll', 'Off']);
    expect(host.search.mock.calls.every(([query]) => query.length <= 255)).toBe(true);
  });

  it('does not overwrite a matching opening when the rest of the quote differs', async () => {
    const host = wordHost(longQuote.replace('reasonable cure period', 'different deadline'));
    expect(await replaceQuoteTracked(longQuote, 'Replacement')).toEqual({ found: false });
    expect(host.changes).toEqual([]);
    expect(host.trackingChanges).toEqual([]);
  });

  it('does not use the first sentence as a replacement fallback', async () => {
    const host = wordHost('Liability is unlimited. An unrelated sentence follows.');
    expect(await replaceQuoteTracked('Liability is unlimited. This clause survives termination.', 'New clause')).toEqual({ found: false });
    expect(host.changes).toEqual([]);
  });

  it('refuses ambiguous short and long clauses without a full explicit selection', async () => {
    for (const quote of ['Liability is unlimited.', longQuote]) {
      const host = wordHost(`${quote}\n\n${quote}`);
      expect(await replaceQuoteTracked(quote, 'New clause')).toEqual({ found: false, reason: 'ambiguous' });
      expect(host.changes).toEqual([]);
    }
  });

  it('uses the selected complete occurrence to disambiguate a repeated clause', async () => {
    const quote = 'Liability is unlimited.';
    const text = `${quote}\n\n${quote}`;
    const host = wordHost(text, { selection: [text.lastIndexOf(quote), text.length] });
    expect((await replaceQuoteTracked(quote, 'Second clause changed.')).found).toBe(true);
    expect(host.text()).toBe(`${quote}\n\nSecond clause changed.`);
    expect(host.search).not.toHaveBeenCalled();
  });

  it('does not treat a partial selection as permission to replace a different passage', async () => {
    const host = wordHost(`${longQuote}\n${longQuote}`, { selection: [0, 60] });
    expect(await replaceQuoteTracked(longQuote, 'New clause')).toEqual({ found: false, reason: 'ambiguous' });
    expect(host.changes).toEqual([]);
  });

  it('retains punctuation and literal carets in the target', async () => {
    const quote = 'The literal codes ^p and ^t are contract text, not paragraph or tab markers.';
    const host = wordHost(quote);
    expect((await replaceQuoteTracked(quote, 'New text')).found).toBe(true);
    expect(host.search.mock.calls[0][0]).toBe('The literal codes ^^p and ^^t are contract text, not paragraph or tab markers.');
    expect(host.changes[0].before).toBe(quote);
  });

  it('tolerates layout whitespace but verifies word boundaries and case', async () => {
    const host = wordHost('A notice\r\n  must be written. After.');
    expect((await replaceQuoteTracked('A notice must be written.', 'Changed.')).found).toBe(true);
    expect(host.changes[0].before).toBe('A notice\r\n  must be written.');
    const wrong = wordHost('An otice must be written. A Notice must be written.');
    expect(await replaceQuoteTracked('A notice must be written.', 'Wrong.')).toEqual({ found: false });
    expect(wrong.changes).toEqual([]);
  });

  it('supports a long passage across paragraphs and Unicode', async () => {
    const quote = `😀 Ελληνικά. ${'First paragraph terms. '.repeat(12)}\n\n${'Пример second paragraph. '.repeat(12)}End of clause.`;
    const host = wordHost(quote.replace('\n\n', '\r'));
    expect((await replaceQuoteTracked(quote, 'New terms 😀.')).found).toBe(true);
    expect(host.text()).toBe('New terms 😀.');
  });

  it('bounds ambiguous candidate expansion while allowing an explicit full selection', async () => {
    const text = Array.from({ length: 20 }, () => longQuote).join('\n\n');
    const host = wordHost(text);
    expect(await replaceQuoteTracked(longQuote, 'Changed.')).toEqual({ found: false, reason: 'too_many_matches' });
    expect(host.changes).toEqual([]);
    const selected = wordHost(text, { selection: [0, longQuote.length] });
    expect((await replaceQuoteTracked(longQuote, 'Changed.')).found).toBe(true);
    expect(selected.changes).toHaveLength(1);
  });

  it('rechecks the live range before enabling tracking', async () => {
    const quote = 'The customer must give notice.';
    const host = wordHost(quote, { sync: count => { if (count === 3) host.change('The customer changed the text.'); } });
    expect(await replaceQuoteTracked(quote, 'Changed.')).toEqual({ found: false, reason: 'changed' });
    expect(host.changes).toEqual([]);
    expect(host.trackingChanges).toEqual([]);
  });

  it('restores the exact previous tracking mode after success and a failed write', async () => {
    const quote = 'The customer must give notice.';
    const host = wordHost(quote, { previous: 'TrackMineOnly' });
    await replaceQuoteTracked(quote, 'Changed.');
    expect(host.trackingChanges).toEqual(['TrackAll', 'TrackMineOnly']);
    const failed = wordHost(quote, { previous: 'TrackAll', sync: count => { if (count === 4) throw new Error('Write failed'); } });
    await expect(replaceQuoteTracked(quote, 'Changed.')).rejects.toThrow('Write failed');
    expect(failed.trackingChanges).toEqual(['TrackAll', 'TrackAll']);
    expect(failed.changes).toEqual([]);
    expect((await replaceQuoteTracked(quote, 'Second attempt.')).found).toBe(true);
  });

  it('reports an applied edit if restoring tracking subsequently fails', async () => {
    const host = wordHost('Complete clause.', { sync: count => { if (count === 5) throw new Error('Restore failed'); } });
    const result = await replaceQuoteTracked('Complete clause.', 'Changed.');
    expect(result.found).toBe(true);
    expect(result.warning).toContain('edit was inserted');
    expect(host.changes).toHaveLength(1);
  });

  it('serializes concurrent edits', async () => {
    const host = wordHost('First clause. Second clause.');
    const [first, second] = await Promise.all([replaceQuoteTracked('First clause.', 'First changed.'), replaceQuoteTracked('Second clause.', 'Second changed.')]);
    expect(first.found && second.found).toBe(true);
    expect(host.text()).toBe('First changed. Second changed.');
    expect(host.maxActive()).toBe(1);
  });
});

describe('literal search edges', () => {
  it('bounds both escaped edges without splitting Unicode', () => {
    const text = '😀^'.repeat(200);
    for (const edge of [quoteEdge(text), quoteEdge(text, true)]) {
      expect(literalQuery(edge).length).toBeLessThanOrEqual(100);
      expect(Array.from(edge).some(c => c.length === 1 && c.charCodeAt(0) >= 0xd800 && c.charCodeAt(0) <= 0xdfff)).toBe(false);
      expect(text.includes(edge)).toBe(true);
    }
    expect(normalizedQuote('  ^p\r\nClause  ')).toBe('^p Clause');
  });
});
