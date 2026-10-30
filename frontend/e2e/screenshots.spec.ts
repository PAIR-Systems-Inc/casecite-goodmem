import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { test, expect, type Page } from '@playwright/test';
import { loginAs, fulfillJson, type MockDocument, type MockOverride } from './helpers/auth';

/**
 * README screenshots. Not a test of anything — it drives the real UI against
 * the e2e API mocks with fictional sample data (firm "Hartwell & Reyes LLP",
 * matter "Marlow v. Tidewater Logistics"; no real people, firms or
 * authorities) and writes PNGs to docs/screenshots/.
 *
 *   SCREENSHOTS=1 PLAYWRIGHT_PORT=3100 npx playwright test e2e/screenshots.spec.ts --project=chromium
 */
const OUT_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../docs/screenshots');

// ---------------------------------------------------------------------------
// Sample knowledge base
// ---------------------------------------------------------------------------
const FOLDER_MATTER = 'Marlow v. Tidewater Logistics';
const FOLDER_CONTRACTS = 'Commercial Contracts';

const doc = (
  id: string, filename: string, folder: string | null, size: number, chunks: number, created: string,
): MockDocument => {
  const ext = filename.split('.').pop();
  const contentType = ext === 'pdf' ? 'application/pdf'
    : ext === 'docx' ? 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'
    : 'text/plain';
  return {
    id, filename, content_type: contentType, size, source: 'upload', status: 'indexed',
    chunk_count: chunks, created_at: created, folder_path: folder,
  };
};

const MSA = doc('doc-msa', 'Marlow-Tidewater_Master-Services-Agreement_2024.pdf', FOLDER_CONTRACTS, 1_884_160, 142, '2026-09-02T14:12:00Z');
const SLA = doc('doc-sla', 'Tidewater_Software-License-Agreement_v3.docx', FOLDER_CONTRACTS, 421_888, 58, '2026-09-02T14:15:00Z');
const LEASE = doc('doc-lease', 'Harbor-Point-Warehouse-Lease_Amendment-2.pdf', FOLDER_CONTRACTS, 987_136, 71, '2026-09-18T09:40:00Z');
const DEPO = doc('doc-depo', 'Deposition-Transcript_D-Okafor_2026-03-14.pdf', FOLDER_MATTER, 3_276_800, 318, '2026-10-01T16:05:00Z');
const MEMO_LIAB = doc('doc-memo-liab', 'Memo_Limitation-of-Liability-Analysis.docx', FOLDER_MATTER, 88_064, 12, '2026-10-06T11:20:00Z');
const COMPLAINT = doc('doc-complaint', 'Marlow_Complaint_Filed-2025-11-02.pdf', FOLDER_MATTER, 1_126_400, 96, '2026-09-30T08:55:00Z');
const INTAKE = doc('doc-intake', 'Client-Intake-Notes_Marlow.txt', null, 14_336, 4, '2026-09-29T17:30:00Z');
const ENGAGEMENT = doc('doc-engagement', 'Hartwell-Reyes_Engagement-Letter_Marlow.pdf', null, 62_464, 9, '2026-09-29T17:34:00Z');

const DOCUMENTS: MockDocument[] = [MSA, SLA, LEASE, DEPO, MEMO_LIAB, COMPLAINT, INTAKE, ENGAGEMENT];
const FOLDERS = [FOLDER_MATTER, FOLDER_CONTRACTS];

/** GET /documents/tree, as MatterFileWorkspace / ContractFilePicker read it. */
function documentTree() {
  const file = (d: MockDocument) => ({
    id: d.id, name: d.filename, path: `${d.folder_path ? d.folder_path + '/' : ''}${d.filename}`,
    type: 'file', size: d.size,
  });
  const folder = (name: string) => {
    const docs = DOCUMENTS.filter((d) => d.folder_path === name);
    return { name, path: name, type: 'folder', document_count: docs.length, children: docs.map(file) };
  };
  return {
    tree: [...FOLDERS.map(folder), ...DOCUMENTS.filter((d) => !d.folder_path).map(file)],
    total_documents: DOCUMENTS.length,
  };
}

/** Folder list, document tree and a friendlier vector-store name. */
const knowledgeBase: MockOverride = async (route, url, method) => {
  if (method !== 'GET') return false;
  const p = url.pathname;
  if (p.endsWith('/documents/folders')) { await fulfillJson(route, 200, { folders: FOLDERS }); return true; }
  if (p.endsWith('/documents/tree')) { await fulfillJson(route, 200, documentTree()); return true; }
  if (p.endsWith('/settings/rag')) { await fulfillJson(route, 200, { vector_db: 'chroma' }); return true; }
  return false;
};

// ---------------------------------------------------------------------------
// Research chat exchange
// ---------------------------------------------------------------------------
const QUESTION = "What does the MSA say about the cap on Tidewater's liability, and does the indemnification carve-out override it?";

const ANSWER = [
  "**Short answer:** Tidewater's liability is capped at the fees paid in the twelve months before the claim, but the cap does not reach indemnified third-party claims.",
  '',
  '- **The cap.** Section 11.2 of the Master Services Agreement limits each party\'s aggregate liability to "the total fees paid or payable under this Agreement in the twelve (12) months preceding the event giving rise to the claim" [1].',
  '- **The carve-out.** Section 11.3 excludes from that cap "a party\'s indemnification obligations under Section 12, any breach of Section 9 (Confidentiality), and liability arising from gross negligence or wilful misconduct" [1].',
  '- **Our prior analysis.** The October memo reads the carve-out as fully uncapped for third-party IP claims and notes that the Software License Agreement contains no equivalent carve-out, so exposure under that agreement stays capped [2][3].',
].join('\n');

const chatResponse = {
  id: 'msg-a1',
  answer: ANSWER,
  content: ANSWER,
  session_id: 'sess-marlow-1',
  stats: { docs_searched: 8, chunks_retrieved: 11, processing_time: '6.4s', case_law_searched: 0, case_law_included: 0 },
  citations: [
    {
      id: 'cite-1', type: 'document', source: MSA.filename, document_id: MSA.id, page: 14,
      confidence: 0.94, similarity: 0.91, verified: true, relevance_rank: 1, chunk_index: 87,
      passage: '11.2 Limitation of Liability. Except as provided in Section 11.3, each party\'s aggregate liability arising out of or relating to this Agreement shall not exceed the total fees paid or payable under this Agreement in the twelve (12) months preceding the event giving rise to the claim.',
      reasoning: [{ type: 'Retrieval', description: 'Section 11.2 states the liability cap.', evidence: 'Exact clause text matched.' }],
    },
    {
      id: 'cite-2', type: 'document', source: MEMO_LIAB.filename, document_id: MEMO_LIAB.id, page: 2,
      confidence: 0.88, similarity: 0.86, verified: true, relevance_rank: 2, chunk_index: 3,
      passage: 'Because Section 11.3 lifts the cap for indemnification obligations, a third-party IP claim against Marlow would be recoverable in full; the data-loss incident is capped unless it is characterised as a confidentiality breach.',
      reasoning: [{ type: 'Retrieval', description: 'Internal memo interprets the carve-out.', evidence: 'Semantic match on carve-out analysis.' }],
    },
    {
      id: 'cite-3', type: 'document', source: SLA.filename, document_id: SLA.id, page: 6,
      confidence: 0.81, similarity: 0.79, verified: true, relevance_rank: 3, chunk_index: 41,
      passage: '9.1 Cap. Licensor\'s total liability under this Agreement shall not exceed the License Fees paid in the twelve (12) months preceding the claim. This limitation applies to all claims, including claims for indemnification.',
      reasoning: [{ type: 'Retrieval', description: 'The license agreement has no carve-out.', evidence: 'Clause applies the cap to indemnification claims.' }],
    },
  ],
};

const chatMock: MockOverride = async (route, url, method) => {
	if (method === 'GET' && url.pathname.endsWith('/jobs/chat-screenshot')) {
	  await fulfillJson(route, 200, { job_id: 'chat-screenshot', status: 'completed', result: chatResponse });
	  return true;
	}
  if (method !== 'POST' || !/\/chat$/.test(url.pathname)) return false;
  await fulfillJson(route, 202, { job_id: 'chat-screenshot', poll_url: '/api/v1/jobs/chat-screenshot' });
  return true;
};

// ---------------------------------------------------------------------------
// Citation check
// ---------------------------------------------------------------------------
const CITATION = '318 Mer. 2d 907';

const validationResult = {
  citation: CITATION,
  case_name: 'Marlow v. Tidewater Logistics, Inc.',
  absolute_url: 'https://example.com/opinions/marlow-v-tidewater-logistics',
  warning_level: 'warning',
  total_citing_cases: 37,
  citing_cases_analyzed: 37,
  positive_citations: 21,
  caution_citations: 4,
  negative_citations: 2,
  last_cited: '2026-06-11',
  citing_cases: [
    {
      id: 1, case_name: 'Okafor Holdings, LLC v. Brightline Freight Co.', citation: '402 Mer. 2d 118',
      court: 'Meridian Court of Appeals, Third District', date_filed: '2026-06-11', treatment: 'negative',
      snippet: 'We decline to follow Marlow to the extent it reads the carve-out to lift the cap for first-party data-loss claims.',
      context_found: true, url: 'https://example.com/opinions/okafor-v-brightline',
    },
    {
      id: 2, case_name: 'Pellman Cold Storage v. Rennick Systems', citation: '396 Mer. 2d 540',
      court: 'Meridian Court of Appeals, First District', date_filed: '2026-02-03', treatment: 'negative',
      snippet: 'Marlow is distinguishable: the indemnity there ran to third-party claims only, and we question its broader dicta.',
      context_found: true, url: 'https://example.com/opinions/pellman-v-rennick',
    },
  ],
  notes: ['Scanned 37 citing opinions for treatment language; negative treatment is concentrated in the Third District.'],
};

const validateCitation: MockOverride = async (route, url, method) => {
  if (method !== 'GET' || !url.pathname.endsWith('/tools/validate-citation')) return false;
  await fulfillJson(route, 200, validationResult);
  return true;
};

// ---------------------------------------------------------------------------
// Contract analysis
// ---------------------------------------------------------------------------
const CONTRACT_TEXT = `SOFTWARE LICENSE AGREEMENT

This Software License Agreement (the "Agreement") is entered into as of 1 March 2024 (the "Effective Date") by and between Tidewater Logistics, Inc., a Delaware corporation ("Licensor"), and Marlow Freight Brokerage LLC, a New Jersey limited liability company ("Licensee").

1. DEFINITIONS

1.1 "Software" means Licensor's RouteLedger dispatch and settlement platform, in object code form, together with any Updates that Licensor makes generally available to its licensees.

1.2 "License Fees" means the fees set out in Schedule A, as adjusted under Section 4.

2. LICENSE GRANT

2.1 Subject to Licensee's compliance with this Agreement, Licensor grants Licensee a non-exclusive, non-transferable license to use the Software for Licensee's internal business operations during the Term.

2.2 Licensee shall not sublicense, rent, lease, or otherwise make the Software available to any third party, except to its contractors acting on its behalf and bound by written obligations no less protective than this Agreement.

3. TERM AND RENEWAL

3.1 This Agreement commences on the Effective Date and continues for an initial term of three (3) years (the "Initial Term").

3.2 Following the Initial Term, this Agreement renews automatically for successive one (1) year periods unless either party gives written notice of non-renewal at least ninety (90) days before the end of the then-current term.

4. FEES AND PAYMENT

4.1 Licensee shall pay the License Fees annually in advance within thirty (30) days of the date of Licensor's invoice.

4.2 Licensor may increase the License Fees on each renewal by notice given no later than one hundred twenty (120) days before the renewal date, provided that no single increase exceeds seven percent (7%).

5. TERMINATION

5.1 Either party may terminate this Agreement on written notice if the other party materially breaches this Agreement and fails to cure the breach within thirty (30) days after receiving notice of it.

5.2 Licensor may suspend Licensee's access to the Software immediately if any undisputed invoice remains unpaid more than fifteen (15) days after its due date.

6. CONFIDENTIALITY

6.1 Each party shall hold the other party's Confidential Information in confidence and shall not disclose it to any third party except to its employees and advisers who need to know it and are bound by obligations of confidentiality. This obligation survives for three (3) years after termination.

7. INTELLECTUAL PROPERTY

7.1 Licensor retains all right, title and interest in and to the Software. Licensee owns all data it enters into or generates through the Software ("Licensee Data").

8. INDEMNIFICATION

8.1 Licensee shall defend and indemnify Licensor against any third-party claim arising from Licensee's use of the Software in breach of this Agreement.

8.2 Licensor shall defend Licensee against any third-party claim that the Software, as delivered, infringes a United States patent or copyright, provided that Licensee gives prompt written notice of the claim and reasonable cooperation.

9. LIMITATION OF LIABILITY

9.1 Cap. Licensor's total liability under this Agreement shall not exceed the License Fees paid in the twelve (12) months preceding the claim. This limitation applies to all claims, including claims for indemnification.

9.2 Exclusions. In no event shall Licensor be liable for any indirect, incidental, special or consequential damages, including loss of profits or loss of data, however caused.

10. GENERAL

10.1 Governing Law. This Agreement is governed by the laws of the State of Delaware, without regard to its conflict-of-laws rules.

10.2 Assignment. Licensee may not assign this Agreement without Licensor's prior written consent. Licensor may assign this Agreement to any affiliate or successor without notice.

10.3 Entire Agreement. This Agreement, including its Schedules, is the entire agreement between the parties on its subject matter.
`;

/** Character span of an exact quote in the contract text. */
function span(quote: string): [number, number] {
  const start = CONTRACT_TEXT.indexOf(quote);
  if (start === -1) throw new Error(`Quote not in contract text: ${quote.slice(0, 40)}`);
  return [start, start + quote.length];
}
const term = (value: string, quote: string) => {
  const [span_start, span_end] = span(quote);
  return { value, quote, span_start, span_end, verified: true };
};

const Q_CAP = "Licensor's total liability under this Agreement shall not exceed the License Fees paid in the twelve (12) months preceding the claim. This limitation applies to all claims, including claims for indemnification.";
const Q_RENEWAL = 'renews automatically for successive one (1) year periods unless either party gives written notice of non-renewal at least ninety (90) days before the end of the then-current term';
const Q_SUSPEND = "Licensor may suspend Licensee's access to the Software immediately if any undisputed invoice remains unpaid more than fifteen (15) days after its due date.";
const Q_ASSIGN = "Licensee may not assign this Agreement without Licensor's prior written consent. Licensor may assign this Agreement to any affiliate or successor without notice.";
const Q_EXCLUSIONS = 'In no event shall Licensor be liable for any indirect, incidental, special or consequential damages, including loss of profits or loss of data, however caused.';

const issue = (
  ref: string, severity: string, title: string, why: string, quote: string | null, suggested: string | null,
) => {
  const [span_start, span_end] = quote ? span(quote) : [null, null];
  return {
    ref, title, status: 'open', severity, why, suggested_language: suggested,
    span_start, span_end, matched_text: quote, clause_slug: null,
    grounding: quote ? 'span' : 'absence',
  };
};

const analysisResult = {
  analysis_id: 'an-2041',
  contract_type: 'license',
  representing: 'Licensee',
  posture: 'Negotiating renewal',
  jurisdiction: 'Delaware',
  executive_summary:
    'A Licensor-favourable form. The liability cap is one-sided and expressly swallows the IP indemnity, so the one protection the Licensee is given in Section 8.2 is capped at a year of fees. Auto-renewal with a 90-day notice window, a 15-day suspension trigger and an asymmetric assignment clause round out the main exposures. There is no obligation on Licensor to return or export Licensee Data on termination.',
  key_terms: {
    effective_date: term('1 March 2024', 'entered into as of 1 March 2024 (the "Effective Date")'),
    term: term('3-year initial term', 'continues for an initial term of three (3) years (the "Initial Term")'),
    renewal: term('Automatic, 1-year periods; 90 days\' notice to stop', Q_RENEWAL),
    termination: term('30-day cure for material breach; suspension after 15 days\' non-payment', 'fails to cure the breach within thirty (30) days after receiving notice of it'),
    payment_terms: term('Annual in advance, net 30; up to 7% increase on renewal', 'pay the License Fees annually in advance within thirty (30) days of the date of Licensor\'s invoice'),
    governing_law: term('Delaware', 'governed by the laws of the State of Delaware'),
    liability_cap: term('12 months\' fees — Licensor only; applies to indemnification', Q_CAP),
    indemnification: term('Mutual but narrow; Licensor covers US patent/copyright only', 'infringes a United States patent or copyright'),
    confidentiality: term('Mutual; survives 3 years', 'This obligation survives for three (3) years after termination.'),
    assignment: term('Licensee needs consent; Licensor free to assign', Q_ASSIGN),
  },
  issues: [
    issue('I-1', 'critical', 'Liability cap applies to the IP indemnity',
      'Section 9.1 caps Licensor\'s liability at twelve months of fees and states that the cap applies to indemnification claims. A single infringement claim against Marlow could exceed that figure many times over, leaving Licensee bearing the excess for a defect in Licensor\'s own product.',
      Q_CAP,
      'Licensor\'s total liability under this Agreement shall not exceed the License Fees paid in the twelve (12) months preceding the claim, except that this limitation shall not apply to Licensor\'s obligations under Section 8.2 or to any breach of Section 6.'),
    issue('I-2', 'critical', 'Cap and exclusions run in one direction only',
      'Both the cap in 9.1 and the consequential-damages exclusion in 9.2 protect Licensor alone. Licensee\'s exposure under its own indemnity in 8.1 is unlimited.',
      Q_EXCLUSIONS,
      'In no event shall either party be liable to the other for any indirect, incidental, special or consequential damages, however caused.'),
    issue('I-3', 'major', 'Automatic renewal with a 90-day notice window',
      'The Agreement renews for a further year unless notice is given 90 days before the end of the term, and Licensor may raise fees by up to 7% on each renewal. Missing the window locks Licensee in for another year at a higher price.',
      Q_RENEWAL,
      'Following the Initial Term, this Agreement renews for successive one (1) year periods only upon the parties\' written agreement.'),
    issue('I-4', 'major', 'No data return or transition assistance on termination',
      'Section 7.1 confirms Licensee owns Licensee Data, but nothing obliges Licensor to export or return it, or to keep the Software available for a wind-down period, after termination or suspension.',
      null,
      'Upon termination or expiry, Licensor shall make Licensee Data available for export in a standard machine-readable format for sixty (60) days and shall provide reasonable transition assistance at its then-current rates.'),
    issue('I-5', 'major', 'Immediate suspension after 15 days',
      'Licensor may cut off a dispatch platform that runs Licensee\'s operations fifteen days after an invoice falls due, with no written warning requirement.',
      Q_SUSPEND,
      'Licensor may suspend Licensee\'s access to the Software if any undisputed invoice remains unpaid more than thirty (30) days after Licensee receives written notice of non-payment.'),
    issue('I-6', 'minor', 'Assignment is asymmetric',
      'Licensee needs consent to assign while Licensor may assign to any affiliate or successor without notice — relevant if Marlow is sold or restructures during the Term.',
      Q_ASSIGN,
      'Either party may assign this Agreement to an affiliate or to a successor to substantially all of its business, on written notice to the other party.'),
  ],
  parties: [
    { canonical_name: 'Tidewater Logistics, Inc.', role: 'Licensor', aliases: ['Licensor'], confidence: 0.99 },
    { canonical_name: 'Marlow Freight Brokerage LLC', role: 'Licensee', aliases: ['Licensee'], confidence: 0.99 },
  ],
  obligations: [
    {
      subject_party: 'Licensee', modal: 'shall', action: 'pay the License Fees annually in advance',
      object_text: 'within 30 days of invoice', category: 'payment', deadlines: ['30 days from invoice'],
      matched_text: 'Licensee shall pay the License Fees annually in advance within thirty (30) days of the date of Licensor\'s invoice.',
      span_start: null, span_end: null,
    },
    {
      subject_party: 'Licensor', modal: 'shall', action: 'defend Licensee against third-party infringement claims',
      object_text: 'US patents and copyrights only', category: 'indemnification', deadlines: [],
      matched_text: 'Licensor shall defend Licensee against any third-party claim that the Software, as delivered, infringes a United States patent or copyright',
      span_start: null, span_end: null,
    },
    {
      subject_party: 'Each party', modal: 'shall', action: 'hold the other party\'s Confidential Information in confidence',
      object_text: null, category: 'confidentiality', deadlines: ['3 years after termination'],
      matched_text: 'Each party shall hold the other party\'s Confidential Information in confidence',
      span_start: null, span_end: null,
    },
  ],
  deadlines: [
    { kind: 'relative_offset', description: 'Non-renewal notice due 90 days before term end', anchor: 'end of current term', offset_days: -90, period_days: null, resolved_date: '2026-12-01', matched_text: 'at least ninety (90) days before the end of the then-current term' },
    { kind: 'relative_offset', description: 'Fee-increase notice from Licensor', anchor: 'renewal date', offset_days: -120, period_days: null, resolved_date: '2026-11-01', matched_text: 'no later than one hundred twenty (120) days before the renewal date' },
    { kind: 'absolute', description: 'Initial Term ends', anchor: null, offset_days: null, period_days: null, resolved_date: '2027-03-01', matched_text: 'initial term of three (3) years' },
  ],
  defined_terms: [],
  summary: { parties: 2, obligations: 3, deadlines: 3 },
};

const pastAnalysis = {
  analysis_id: analysisResult.analysis_id, document_id: SLA.id, contract_type: 'license',
  created_at: '2026-10-28T10:12:00Z', issues: analysisResult.issues.length,
};

const contractsMock: MockOverride = async (route, url, method) => {
  const p = url.pathname;
  if (method === 'GET' && p.endsWith(`/documents/${SLA.id}/content`)) {
    await fulfillJson(route, 200, { document_id: SLA.id, filename: SLA.filename, text: CONTRACT_TEXT });
    return true;
  }
  // No native file render: the viewer falls back to the extracted text.
  if (method === 'GET' && /\/documents\/[^/]+\/file$/.test(p)) {
    await fulfillJson(route, 404, { detail: 'No stored file' });
    return true;
  }
  if (method === 'GET' && p.endsWith('/contract-analysis/analyses')) {
    await fulfillJson(route, 200, { analyses: [pastAnalysis] });
    return true;
  }
  if (method === 'POST' && p.endsWith('/contract-analysis/chat')) {
    await fulfillJson(route, 202, { type: 'analysis_started', job_id: 'job-2041', poll_url: '/jobs/job-2041' });
    return true;
  }
  if (method === 'GET' && p.endsWith('/jobs/job-2041')) {
    await fulfillJson(route, 200, { job_id: 'job-2041', status: 'completed', result: analysisResult });
    return true;
  }
  return false;
};

// ---------------------------------------------------------------------------

async function shoot(page: Page, name: string) {
  fs.mkdirSync(OUT_DIR, { recursive: true });
  // Let fonts, icons and entry transitions settle before the capture.
  await page.waitForTimeout(600);
  await page.screenshot({ path: path.join(OUT_DIR, name), fullPage: false });
}

test.describe('README screenshots', () => {
  test.skip(!process.env.SCREENSHOTS, 'README screenshots; run with SCREENSHOTS=1');
  test.use({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 });

  test('knowledge-base.png', async ({ page }) => {
    await loginAs(page, '/documents', { documents: DOCUMENTS, overrides: [knowledgeBase] });
    const main = page.locator('main[data-layout="main-content"]');
    await expect(main.getByRole('heading', { name: 'Knowledge Base' })).toBeVisible();
    await expect(main.getByText(ENGAGEMENT.filename)).toBeVisible();

    // Folders start collapsed — open both so their documents show.
    for (const folder of FOLDERS) {
      await main.locator('[class*="folderToggle"]', { hasText: folder }).click();
    }
    await expect(main.getByText(DEPO.filename)).toBeVisible();
    await expect(main.getByText(MSA.filename)).toBeVisible();
    await expect(page.locator('header[data-layout="header"]')).toContainText(`${DOCUMENTS.length} documents indexed`);

    await shoot(page, 'knowledge-base.png');
  });

  test('research.png', async ({ page }) => {
    await loginAs(page, '/research', { documents: DOCUMENTS, overrides: [knowledgeBase, chatMock] });
    const main = page.locator('main[data-layout="main-content"]');
    await expect(main.getByRole('heading', { name: 'Matter Strategy' })).toBeVisible();
    await expect(main.getByText(MSA.filename)).toBeVisible();

    await main.getByLabel('Ask or strategize across your documents').fill(QUESTION);
    await main.getByRole('button', { name: 'Send question' }).click();

    await expect(main.getByText('Sources (3)')).toBeVisible({ timeout: 15_000 });
    await expect(main.getByText('Short answer:')).toBeVisible();
    await expect(main.getByRole('note').first()).toBeVisible();
    await expect(main.locator('[class*="processingBubble"]')).toHaveCount(0);
    // The dock lands on the top of the answer; show the question above it too.
    await main.locator('[class*="userBubble"]').scrollIntoViewIfNeeded();

    await shoot(page, 'research.png');
  });

  test('citation-check.png', async ({ page }) => {
    await loginAs(page, '/tools', { documents: DOCUMENTS, overrides: [knowledgeBase, validateCitation] });
    await expect(page.getByRole('heading', { name: 'Legal Tools' })).toBeVisible();
    await page.locator('aside[data-layout="left-sidebar"]').getByRole('button', { name: /Citation Check/ }).click();
    await expect(page.getByRole('heading', { name: 'Citation Check' })).toBeVisible();

    await page.getByLabel('Citation Check search').fill(CITATION);
    await page.keyboard.press('Enter');

    const main = page.locator('main[data-layout="main-content"]');
    const card = main.locator('[class*="resultCard"]');
    await expect(card).toBeVisible({ timeout: 15_000 });
    await expect(card).toContainText('Negative language found');
    await expect(card.getByRole('note')).toBeVisible();

    await shoot(page, 'citation-check.png');
  });

  test('contracts.png', async ({ page }) => {
    await loginAs(page, '/contracts', { documents: DOCUMENTS, overrides: [knowledgeBase, contractsMock] });
    const main = page.locator('main[data-layout="main-content"]');
    await expect(main.getByRole('heading', { name: 'Contracts' })).toBeVisible();

    // Pick the license agreement from the document browser.
    await main.getByRole('button', { name: 'Select a file' }).click();
    const picker = page.getByRole('dialog');
    await expect(picker.getByRole('heading', { name: 'Browse Documents' })).toBeVisible();
    await picker.getByText(SLA.filename).click();
    await picker.getByRole('button', { name: 'Load Document' }).click();
    await expect(main.getByText('SOFTWARE LICENSE AGREEMENT').first()).toBeVisible({ timeout: 15_000 });

    // Ask for a review: 202 → job poll → analysis card in the conversation dock.
    const composer = main.getByLabel('Ask about the selected files');
    await composer.fill("Review this agreement from the Licensee's side — what should we push back on at renewal?");
    await composer.press('Enter');

    await expect(main.getByText('Contract Analysis', { exact: true })).toBeVisible({ timeout: 30_000 });
    await expect(main.getByText('Executive Summary')).toBeVisible();
    await expect(main.locator('[class*="processingBubble"]')).toHaveCount(0);

    await shoot(page, 'contracts.png');
  });
});
