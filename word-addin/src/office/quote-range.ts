import { WORD_SEARCH_LIMIT } from './search';

export type QuoteFailure = 'ambiguous' | 'too_many_matches' | 'changed';
type QuoteMatch = { range: Word.Range; matched: string } | { reason?: QuoteFailure };

// Do not discard punctuation, case or carets when verifying a replacement.
export function normalizedQuote(text: string): string {
  return text.replace(/\s+/g, ' ').trim();
}

/** Literal Word search, including its caret escape syntax. */
export function literalQuery(text: string): string {
  return text.replace(/\^/g, '^^');
}

// Work in code points so a limit never splits an emoji/surrogate pair. The
// limit applies after Word escaping, not just to the original source string.
export function quoteEdge(text: string, fromEnd = false): string {
  const characters = Array.from(text);
  if (fromEnd) characters.reverse();
  const result: string[] = [];
  let length = 0;
  for (const character of characters) {
    const size = literalQuery(character).length;
    if (length + size > 100) break;
    result.push(character);
    length += size;
  }
  if (fromEnd) result.reverse();
  return result.join('').trim();
}

/** Find the whole passage. Never use the navigation helper's shorter fallback
 * as the target of a destructive replacement. WordApi 1.3 range operations are
 * within the add-in's existing 1.4 requirement. */
export async function exactQuoteRange(context: Word.RequestContext, quote: string | null | undefined): Promise<QuoteMatch> {
  const wanted = normalizedQuote(quote ?? '');
  if (!wanted) return {};

  // An explicit full selection disambiguates repeated clauses, including very
  // common fragments for which bounded global searching would be expensive.
  const selection = context.document.getSelection();
  selection.load('text');
  await context.sync();
  if (normalizedQuote(selection.text) === wanted) return { range: selection, matched: selection.text };

  const options = { matchCase: true, matchWildcards: false, ignoreSpace: true, ignorePunct: false };
  const query = literalQuery(wanted);
  let candidates: Word.Range[];
  if (query.length <= WORD_SEARCH_LIMIT) {
    const results = context.document.body.search(query, options);
    results.load('items/text');
    await context.sync();
    if (results.items.length > 128) return { reason: 'too_many_matches' };
    candidates = results.items;
  } else {
    // Word caps individual searches; joining the first and last ranges and
    // verifying all intervening text handles long and multi-paragraph clauses.
    const starts = context.document.body.search(literalQuery(quoteEdge(wanted)), options);
    const ends = context.document.body.search(literalQuery(quoteEdge(wanted, true)), options);
    starts.load('items'); ends.load('items');
    await context.sync();
    if (starts.items.length * ends.items.length > 256) return { reason: 'too_many_matches' };
    const pairs = starts.items.flatMap(start => ends.items.map(end => ({ start, end, location: start.compareLocationWith(end) })));
    await context.sync();
    candidates = [];
    for (const pair of pairs) {
      if (pair.location.value !== 'Before' && pair.location.value !== 'OverlapsBefore' && pair.location.value !== 'Contains') continue;
      const range = pair.start.expandToOrNullObject(pair.end);
      range.load('text');
      candidates.push(range);
    }
    await context.sync();
  }
  const matches = candidates.filter(range => !range.isNullObject && normalizedQuote(range.text) === wanted);
  if (matches.length > 1) return { reason: 'ambiguous' };
  return matches.length ? { range: matches[0], matched: matches[0].text } : {};
}
