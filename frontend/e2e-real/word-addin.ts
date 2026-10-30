import { readFile } from 'node:fs/promises';
import { expect, type APIRequestContext, type Page, type TestInfo } from '@playwright/test';

export async function exerciseWordAddin(page: Page, request: APIRequestContext, headers: Record<string, string>, testInfo: TestInfo) {
  const fixture = JSON.parse(await readFile('../server/internal/app/testdata/word_contract.json', 'utf8')) as { title: string; quote: string; replacement: string };
  const text = `${fixture.title}\n\nFirst agreement.\n${fixture.quote}\n\nSecond agreement.\n${fixture.quote}\n\nEnd of document.`;
  const matter = await request.post('/api/v1/matters', { headers, data: { name: 'Word add-in matter' } });
  expect(matter.status(), await matter.text()).toBe(200);
  const matterID = (await matter.json()).id as string;
  const upload = await request.post('/api/v1/documents', { headers, multipart: {
    matter_id: matterID,
    file: { name: 'word-research.txt', mimeType: 'text/plain', buffer: Buffer.from('Notice is 30 days. The customer must deliver notice in writing.') },
  } });
  expect(upload.status(), await upload.text()).toBe(200);
  const documentID = (await upload.json()).id as string;
  await expect.poll(async () => (await (await request.get(`/api/v1/documents/${documentID}`, { headers })).json()).status, { timeout: 90_000 }).toBe('indexed');

  const pane = await page.context().newPage();
  pane.setDefaultTimeout(15_000);
  // Simulate only the Office bridge. The embedded pane, authentication, Go
  // analysis/chat endpoints, database and GoodMem are the deployed test stack.
  await pane.route('https://appsforoffice.microsoft.com/lib/1/hosted/office.js', route => route.fulfill({ contentType: 'text/javascript', body: '/* Office bridge supplied by the isolated test host. */' }));
  await pane.addInitScript(source => {
    const state = {
      text: source, selection: [0, 0], tracking: 'Off', modes: [] as string[],
      edits: [] as { before: string; after: string; mode: string }[], paragraphs: [] as string[], footnotes: [] as string[],
    };
    type Marker = { insertFootnote: (text: string) => { reference: { getRange: () => Marker } } };
    const marker: Marker = { insertFootnote: value => { state.footnotes.push(value); return { reference: { getRange: () => marker } }; } };
    type Paragraph = { font: { bold: boolean; italic: boolean }; insertParagraph: (value: string) => Paragraph; getRange: () => Marker };
    const paragraph = (value: string): Paragraph => { state.paragraphs.push(value); return { font: { bold: false, italic: false }, insertParagraph: paragraph, getRange: () => marker }; };
    type Range = {
      start: number; end: number; text: string; isNullObject: boolean; load: () => void; select: () => void;
      insertText: (value: string) => Range; insertParagraph: (value: string) => Paragraph;
      compareLocationWith: (other: Range) => { value: string }; expandToOrNullObject: (other: Range) => Range;
    };
    const range = (start: number, end: number): Range => ({
      start, end, get text() { return state.text.slice(start, end); }, isNullObject: false, load: () => {},
      select: () => { state.selection = [start, end]; },
      insertText: value => {
        state.edits.push({ before: state.text.slice(start, end), after: value, mode: state.tracking });
        state.text = state.text.slice(0, start) + value + state.text.slice(end);
        return range(start, start + value.length);
      },
      insertParagraph: paragraph,
      compareLocationWith: other => ({ value: end <= other.start ? 'Before' : start >= other.end ? 'After' : start < other.start ? 'OverlapsBefore' : 'OverlapsAfter' }),
      expandToOrNullObject: other => range(Math.min(start, other.start), Math.max(end, other.end)),
    });
    const body = {
      load: () => {}, get text() { return state.text; },
      search: (query: string) => {
        const literal = query.replace(/\^\^/g, '^').replace(/\s/g, '');
        const positions: number[] = [];
        let compact = '';
        for (let i = 0; i < state.text.length; i++) if (!/\s/.test(state.text[i])) { compact += state.text[i]; positions.push(i); }
        const items: Range[] = [];
        for (let from = 0; literal && from < compact.length;) {
          const found = compact.indexOf(literal, from);
          if (found < 0) break;
          items.push(range(positions[found], positions[found + literal.length - 1] + 1));
          from = found + literal.length;
        }
        return { items, load: () => {} };
      },
    };
    const host = window as unknown as Record<string, unknown>;
    host.caseciteWordHost = state;
    host.Office = { onReady: (ready: () => void) => ready(), context: { requirements: { isSetSupported: () => true }, ui: { openBrowserWindow: () => {} } } };
    host.Word = { ChangeTrackingMode: { trackAll: 'TrackAll' }, run: async (action: (context: unknown) => Promise<unknown>) => action({
      document: { body, load: () => {}, getSelection: () => range(state.selection[0], state.selection[1]),
        get changeTrackingMode() { return state.tracking; }, set changeTrackingMode(mode: string) { state.modes.push(mode); state.tracking = mode; },
      }, sync: async () => {},
    }) };
  }, text);
  await pane.setViewportSize({ width: 390, height: 844 });
  await pane.goto('/word-addin/');
  await expect(pane.getByLabel('Server address', { exact: true })).toHaveValue(new URL(page.url()).origin);
  await pane.getByRole('button', { name: 'Sign in', exact: true }).click();
  const device = pane.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/auth/device/start');
  await pane.getByRole('button', { name: 'Get a sign-in code', exact: true }).click();
  const code = await (await device).json();
  await expect(pane.getByText(code.user_code, { exact: true })).toBeVisible();
  const approved = await request.post('/api/v1/auth/device/approve', { headers, data: { user_code: code.user_code } });
  expect(approved.status(), await approved.text()).toBe(200);
  await expect(pane.getByRole('heading', { name: 'Analyze this document' })).toBeVisible({ timeout: 20_000 });
  const analysis = pane.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/contract-analysis/analyze');
  await pane.locator('main').getByRole('button', { name: 'Analyze', exact: true }).click();
  const response = await analysis;
  expect(response.status(), await response.text()).toBe(200);
  const result = await response.json();
  expect(result.issues).toEqual(expect.arrayContaining([expect.objectContaining({ title: 'Review indemnity scope', matched_text: fixture.quote, grounding: 'span', suggested_language: fixture.replacement })]));
  const issue = pane.getByRole('listitem').filter({ hasText: 'Review indemnity scope' });
  await issue.getByRole('button', { name: 'Insert as tracked change', exact: true }).click();
  await expect(pane.getByText(/Select the complete intended passage/)).toBeVisible();
  await pane.evaluate(quote => {
    const state = (window as unknown as { caseciteWordHost: { text: string; selection: number[]; edits: unknown[] } }).caseciteWordHost;
    if (state.edits.length) throw new Error('Ambiguous source was changed');
    const start = state.text.lastIndexOf(quote);
    state.selection = [start, start + quote.length];
  }, fixture.quote);
  await issue.getByRole('button', { name: 'Insert as tracked change', exact: true }).click();
  await expect(pane.getByText(/Inserted as a tracked change/)).toBeVisible();
  await expect(issue.getByRole('button', { name: 'Tracked change inserted', exact: true })).toBeDisabled();
  const host = await pane.evaluate(() => (window as unknown as { caseciteWordHost: { text: string; tracking: string; edits: unknown[]; modes: string[] } }).caseciteWordHost);
  expect(host.edits).toEqual([{ before: fixture.quote, after: fixture.replacement, mode: 'TrackAll' }]);
  expect(host.text).toBe(text.slice(0, text.lastIndexOf(fixture.quote)) + fixture.replacement + '\n\nEnd of document.');
  expect(host.tracking).toBe('Off');
  expect(host.modes).toEqual(['TrackAll', 'Off']);
  await pane.screenshot({ path: testInfo.outputPath('word-addin-analysis.png'), fullPage: true });

  await pane.getByRole('button', { name: 'Research', exact: true }).click();
  await pane.getByLabel('Search within', { exact: true }).selectOption(matterID);
  await pane.getByLabel('Question', { exact: true }).fill('What notice does the agreement require?');
  const research = pane.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/chat');
  await pane.getByRole('button', { name: 'Ask', exact: true }).click();
  const answer = await research;
  expect(answer.status(), await answer.text()).toBe(200);
  expect((await answer.json()).citations.length).toBeGreaterThan(0);
  await expect(pane.getByRole('listitem').filter({ hasText: 'word-research.txt' })).toBeVisible();
  await pane.getByRole('button', { name: 'Insert at cursor with footnotes', exact: true }).click();
  await expect(pane.getByText('Inserted at the cursor with the sources as footnotes.')).toBeVisible();
  const inserted = await pane.evaluate(() => (window as unknown as { caseciteWordHost: { footnotes: string[]; paragraphs: string[] } }).caseciteWordHost);
  expect(inserted.footnotes.join('\n')).toContain('word-research.txt');
  expect(inserted.paragraphs.join('\n')).toContain('30 days');
  await pane.screenshot({ path: testInfo.outputPath('word-addin-research.png'), fullPage: true });
  await pane.getByRole('button', { name: 'Settings', exact: true }).click();
  await pane.getByRole('button', { name: 'Sign out', exact: true }).click();
  await expect(pane.getByText('Not signed in.', { exact: true })).toBeVisible();
  await pane.close();
}
