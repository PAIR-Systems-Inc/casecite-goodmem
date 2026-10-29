import type { Citation } from '../api/types';
import { searchCandidates } from './search';

/**
 * Everything the pane does to the open document, behind `Word.run`.
 * Each function is a complete batch so a failure leaves the document as it was.
 */

export interface SelectionResult {
  found: boolean;
  /** The search string that matched, for the UI to say what was selected. */
  matched?: string;
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
  return Word.run(async (context) => {
    const doc = context.document;
    doc.load('changeTrackingMode');
    await context.sync();
    const previous = doc.changeTrackingMode;

    const hit = await findRange(context, quote);
    if (!hit) return { found: false };

    doc.changeTrackingMode = Word.ChangeTrackingMode.trackAll;
    try {
      const inserted = hit.range.insertText(replacement, 'Replace');
      inserted.select('Select');
      await context.sync();
    } finally {
      doc.changeTrackingMode = previous;
      await context.sync();
    }
    return { found: true, matched: hit.matched };
  });
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
): Promise<void> {
  const paragraphs = answer
    .split(/\n{2,}|\r?\n/)
    .map((p) => p.trim())
    .filter(Boolean);
  const useFootnotes = footnotesSupported();

  return Word.run(async (context) => {
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
  });
}
