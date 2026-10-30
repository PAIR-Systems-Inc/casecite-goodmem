import type { Citation } from '../api/types';
import { searchCandidates } from './search';
import { exactQuoteRange, normalizedQuote, type QuoteFailure } from './quote-range';

/**
 * Everything the pane does to the open document, behind `Word.run`.
 * Mutations run in sequence so concurrent pane actions cannot change each
 * other's selection or tracking mode. Office batches are not transactions.
 */

export interface SelectionResult {
  found: boolean;
  /** The search string that matched, for the UI to say what was selected. */
  matched?: string;
  reason?: QuoteFailure;
  warning?: string;
}

let documentWrites: Promise<unknown> = Promise.resolve();
function writeDocument<T>(operation: () => Promise<T>): Promise<T> {
  const result = documentWrites.then(operation, operation);
  documentWrites = result.then(() => undefined, () => undefined);
  return result;
}

/** The body text as Word reports it (what the analysis is run on). */
export async function readDocumentText(): Promise<string> {
  return Word.run(async (context) => {
    const body = context.document.body;
    body.load('text');
    await context.sync();
    return body.text ?? '';
  });
}

async function findRange(
  context: Word.RequestContext,
  quote: string | null | undefined,
): Promise<{ range: Word.Range; matched: string } | null> {
  for (const candidate of searchCandidates(quote)) {
    const results = context.document.body.search(candidate, {
      matchCase: false,
      matchWildcards: false,
      ignoreSpace: true,
      ignorePunct: false,
    });
    results.load('items');
    await context.sync();
    if (results.items.length > 0) return { range: results.items[0], matched: candidate };
  }
  return null;
}

/** Select the passage an issue or key term quotes, falling back to shorter searches. */
export async function selectQuote(quote: string | null | undefined): Promise<SelectionResult> {
  return Word.run(async (context) => {
    const hit = await findRange(context, quote);
    if (!hit) return { found: false };
    hit.range.select('Select');
    await context.sync();
    return { found: true, matched: hit.matched };
  });
}

/**
 * Replace the quoted passage with `replacement` as a tracked change, whatever
 * the document's own tracking setting is, and put that setting back afterwards
 * so the lawyer's own typing is tracked exactly as before.
 */
export async function replaceQuoteTracked(
  quote: string | null | undefined,
  replacement: string,
): Promise<SelectionResult> {
  return writeDocument(() => Word.run(async (context) => {
    const doc = context.document;
    const hit = await exactQuoteRange(context, quote);
    if (!('range' in hit)) return hit.reason ? { found: false, reason: hit.reason } : { found: false };

    // Re-read immediately before writing: a coauthor or the user may have
    // changed the live range while the search was in flight.
    hit.range.load('text');
    doc.load('changeTrackingMode');
    await context.sync();
    if (normalizedQuote(hit.range.text) !== normalizedQuote(quote ?? '')) return { found: false, reason: 'changed' };
    const previous = doc.changeTrackingMode;
    let applied = false;
    let writeFailed = false;
    let writeError: unknown;
    let warning: string | undefined;
    try {
      doc.changeTrackingMode = Word.ChangeTrackingMode.trackAll;
      const inserted = hit.range.insertText(replacement, 'Replace');
      inserted.select('Select');
      await context.sync();
      applied = true;
    } catch (error) {
      writeFailed = true;
      writeError = error;
    }
    try {
      doc.changeTrackingMode = previous;
      await context.sync();
    } catch (error) {
      if (applied) {
        warning = 'The edit was inserted, but the previous tracking mode could not be restored. Check Review → Track Changes in Word.';
      } else if (!writeFailed) {
        writeFailed = true;
        writeError = error;
      }
    }
    if (writeFailed) throw writeError;
    return { found: true, matched: hit.matched, ...(warning ? { warning } : {}) };
  }));
}

function footnotesSupported(): boolean {
  try {
    return Office.context.requirements.isSetSupported('WordApi', '1.5');
  } catch {
    return false;
  }
}

/** One line per citation for a footnote or the Sources list. */
export function citationLine(citation: Citation): string {
  const passage = (citation.passage || '').replace(/\s+/g, ' ').trim();
  const excerpt = passage.length > 240 ? `${passage.slice(0, 240).trimEnd()}…` : passage;
  const parts = [citation.source || 'Source'];
  if (excerpt) parts.push(`“${excerpt}”`);
  if (citation.url) parts.push(citation.url);
  return parts.join(' — ');
}

/**
 * Insert a research answer at the selection: one paragraph per answer
 * paragraph, a footnote per citation on the last paragraph (or a Sources list
 * where footnotes are not supported), then the AI notice in italics.
 */
export async function insertAnswerWithFootnotes(
  answer: string,
  citations: Citation[],
  notice: string,
): Promise<'footnotes' | 'sources' | 'none'> {
  const paragraphs = answer
    .split(/\n{2,}|\r?\n/)
    .map((p) => p.trim())
    .filter(Boolean);
  const useFootnotes = footnotesSupported();

  await writeDocument(() => Word.run(async (context) => {
    const selection = context.document.getSelection();
    let last: Word.Paragraph | null = null;
    let anchor: Word.Range | Word.Paragraph = selection;
    for (const text of paragraphs.length ? paragraphs : [answer.trim()]) {
      last = anchor.insertParagraph(text, 'After');
      anchor = last;
    }
    if (!last) return;

    if (citations.length && useFootnotes) {
      // Footnotes hang off the end of the last answer paragraph, in order.
      let marker = last.getRange('End');
      for (const citation of citations) {
        const note = marker.insertFootnote(citationLine(citation));
        marker = note.reference.getRange('After');
      }
    } else if (citations.length) {
      const heading = last.insertParagraph('Sources', 'After');
      heading.font.bold = true;
      let cursor: Word.Paragraph = heading;
      citations.forEach((citation, index) => {
        cursor = cursor.insertParagraph(`[${index + 1}] ${citationLine(citation)}`, 'After');
        cursor.font.bold = false;
      });
      last = cursor;
    }

    const noticePara = last.insertParagraph(notice, 'After');
    noticePara.font.italic = true;
    noticePara.font.bold = false;
    await context.sync();
  }));
  return citations.length ? useFootnotes ? 'footnotes' : 'sources' : 'none';
}
