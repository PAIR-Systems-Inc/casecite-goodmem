import { describe, it, expect } from 'vitest';
import { WORD_SEARCH_LIMIT, normalizeForSearch, searchCandidates, truncateForSearch } from './search';

describe('normalizeForSearch', () => {
  it('collapses whitespace and removes the caret Word treats as an escape', () => {
    expect(normalizeForSearch('  The  Supplier\n\tshall ^ indemnify ')).toBe('The Supplier shall indemnify');
  });
});

describe('truncateForSearch', () => {
  it('leaves short text alone', () => {
    expect(truncateForSearch('Short clause.')).toBe('Short clause.');
  });

  it('cuts at a word boundary within the 255-character limit', () => {
    const long = Array.from({ length: 60 }, (_, i) => `word${i}`).join(' ');
    const cut = truncateForSearch(long);
    expect(cut.length).toBeLessThanOrEqual(WORD_SEARCH_LIMIT);
    expect(long.startsWith(cut)).toBe(true);
    expect(long.charAt(cut.length)).toBe(' ');
  });

  it('falls back to a hard cut when there is no usable space', () => {
    const noSpaces = 'x'.repeat(300);
    expect(truncateForSearch(noSpaces)).toHaveLength(WORD_SEARCH_LIMIT);
  });
});

describe('searchCandidates', () => {
  it('returns nothing for an empty quote', () => {
    expect(searchCandidates(null)).toEqual([]);
    expect(searchCandidates('   ')).toEqual([]);
  });

  it('offers the whole quote first, then its first sentence, then a short opening', () => {
    const quote =
      'The Supplier shall indemnify the Customer against all losses arising from a breach of clause 12. ' +
      'This indemnity survives termination of the Agreement for a period of six years.';
    const candidates = searchCandidates(quote);
    expect(candidates[0]).toBe(quote);
    expect(candidates[1]).toBe(
      'The Supplier shall indemnify the Customer against all losses arising from a breach of clause 12.',
    );
    expect(candidates[2].length).toBeLessThanOrEqual(80);
    expect(quote.startsWith(candidates[2])).toBe(true);
    expect(new Set(candidates).size).toBe(candidates.length);
  });

  it('never exceeds the Word limit and never duplicates a candidate', () => {
    const quote = `${'Liability '.repeat(40)}is capped at the fees paid.`;
    for (const candidate of searchCandidates(quote)) {
      expect(candidate.length).toBeLessThanOrEqual(WORD_SEARCH_LIMIT);
    }
    const short = 'Governing law: England.';
    expect(searchCandidates(short)).toEqual([short]);
  });
});
