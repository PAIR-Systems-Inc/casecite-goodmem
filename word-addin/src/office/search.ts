/**
 * Turning a quote from the analysis into something Word's `body.search` can
 * find. Word caps a search string at 255 characters and treats `^` as an
 * escape, and the text the API quotes comes from the extracted body, where
 * runs of whitespace may differ from the document.
 */

export const WORD_SEARCH_LIMIT = 255;

/** Collapse whitespace and drop characters Word's search cannot take literally. */
export function normalizeForSearch(text: string): string {
  return text.replace(/\^/g, ' ').replace(/\s+/g, ' ').trim();
}

/** Cut to the limit at a word boundary, so the search never ends mid-word. */
export function truncateForSearch(text: string, limit = WORD_SEARCH_LIMIT): string {
  const clean = normalizeForSearch(text);
  if (clean.length <= limit) return clean;
  const cut = clean.slice(0, limit);
  const lastSpace = cut.lastIndexOf(' ');
  return (lastSpace > limit * 0.6 ? cut.slice(0, lastSpace) : cut).trim();
}

/**
 * Candidate search strings, most specific first: the whole quote when it fits,
 * else its first 255 characters; then its first sentence; then a shorter
 * opening fragment. Duplicates and fragments too short to be unique are left out.
 */
export function searchCandidates(quote: string | null | undefined): string[] {
  const clean = normalizeForSearch(quote ?? '');
  if (!clean) return [];
  const out: string[] = [];
  const push = (value: string) => {
    const v = value.trim();
    if (v.length >= 12 && !out.includes(v)) out.push(v);
  };
  push(truncateForSearch(clean));
  const sentence = clean.match(/^[^.!?]+[.!?]?/);
  if (sentence) push(truncateForSearch(sentence[0]));
  push(truncateForSearch(clean, 80));
  if (out.length === 0 && clean.length >= 4) out.push(truncateForSearch(clean));
  return out;
}
