import { readFile, writeFile } from 'node:fs/promises';
import { expect, type APIRequestContext, type Page, type TestInfo } from '@playwright/test';

export async function exerciseDocumentFormats(page: Page, request: APIRequestContext, headers: Record<string, string>, testInfo: TestInfo) {
  const corpus = JSON.parse(await readFile('../server/internal/app/testdata/text_extraction.json', 'utf8')) as {
    cases: Array<{ name: string; content: string; text: string; improved_text?: string; tables: number; pages: number }>;
  };
  for (const name of ['agreement.docx', 'workbook.xlsx', 'workbook.xls', 'presentation.pptx', 'legacy.doc', 'message.msg', 'message.eml']) {
    const fixture = corpus.cases.find(c => c.name === name)!;
    const response = await request.post('/api/v1/legal-docs/extract-text', {
      headers, multipart: { file: { name, mimeType: 'application/octet-stream', buffer: Buffer.from(fixture.content, 'base64') } },
    });
    expect(response.status(), await response.text()).toBe(200);
    expect(await response.json()).toMatchObject({ success: true, text: fixture.improved_text ?? fixture.text, tables_found: fixture.tables, page_count: fixture.pages, ocr_used: false, truncated: false });
  }
  const pdfTables = JSON.parse(await readFile('../server/internal/app/testdata/pdf_tables.json', 'utf8')) as {
    cases: Array<{ name: string; content: string; tables: string[]; needs_ocr: boolean }>;
  };
  for (const fixture of pdfTables.cases.filter(c => !c.needs_ocr)) {
    const response = await request.post('/api/v1/legal-docs/extract-text', {
      headers, multipart: { file: { name: `${fixture.name}.pdf`, mimeType: 'application/pdf', buffer: Buffer.from(fixture.content, 'base64') } },
    });
    expect(response.status(), await response.text()).toBe(200);
    const extracted = await response.json();
    expect(extracted).toMatchObject({ tables_found: fixture.tables.length, ocr_used: false, page_count: 1 });
    if (fixture.name !== 'literal-punctuation') {
      for (const table of fixture.tables) expect(extracted.text).toContain(table);
    } else {
      expect(extracted.text).toContain('A\\|B and C\\\\D');
    }
  }
  const source = corpus.cases.find(c => c.name === 'agreement.docx')!;
  // Inspect the actual Blob handed to the viewer. Chromium's network body
  // capture can omit inline PDF bytes, and CSP intentionally disallows a new
  // fetch of blob: URLs. The normal URL creation and viewer remain untouched.
  await page.addInitScript(() => {
    const blobs = new Map<string, Blob>();
    (window as unknown as { caseciteTestBlobs: Map<string, Blob> }).caseciteTestBlobs = blobs;
    const create = URL.createObjectURL.bind(URL);
    URL.createObjectURL = (object: Blob | MediaSource) => {
      const url = create(object);
      if (object instanceof Blob) blobs.set(url, object);
      return url;
    };
  });
  await page.goto('/contracts');
  const conversion = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/legal-docs/convert-to-pdf');
  const upload = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/documents');
  await page.getByLabel('Upload a contract', { exact: true }).setInputFiles({ name: source.name, mimeType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', buffer: Buffer.from(source.content, 'base64') });
  const uploaded = await upload;
  expect(uploaded.status()).toBe(200);
  const doc = await uploaded.json() as { id: string };
  const preview = await conversion;
  expect(preview.status()).toBe(200);
  expect(preview.headers()['content-type']).toBe('application/pdf');
  await writeFile(testInfo.outputPath('word-original.docx'), Buffer.from(source.content, 'base64'));
  const viewer = page.getByRole('complementary', { name: 'Contract text', exact: true });
  await expect(viewer.getByRole('button', { name: 'Document', exact: true })).toBeVisible();
  await expect(viewer.getByTitle('Document viewer', { exact: true })).toBeVisible();
  const blobURL = (await viewer.getByTitle('Document viewer', { exact: true }).getAttribute('src'))!.split('#')[0];
  const pdf = Buffer.from(await page.evaluate(async url => {
    const blob = (window as unknown as { caseciteTestBlobs: Map<string, Blob> }).caseciteTestBlobs.get(url);
    if (!blob) throw new Error('The viewer did not receive a Blob');
    return Array.from(new Uint8Array(await blob.arrayBuffer()));
  }, blobURL));
  expect(pdf.subarray(0, 5).toString()).toBe('%PDF-');
  await writeFile(testInfo.outputPath('word-preview.pdf'), pdf);
  await viewer.getByRole('button', { name: 'Text', exact: true }).click();
  await expect(viewer).toContainText('Payment is due in thirty days.');
  await expect(viewer).toContainText('End of agreement.');
  await expect(viewer.locator('p').filter({ hasText: 'Payment is due in thirty days.' })).not.toContainText('End of agreement.');
  await page.screenshot({ path: testInfo.outputPath('word-contract-desktop.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(viewer).toBeVisible();
  const exportButton = await viewer.getByRole('button', { name: 'Export to Word', exact: true }).boundingBox();
  expect(exportButton!.x + exportButton!.width).toBeLessThanOrEqual(390);
  await page.screenshot({ path: testInfo.outputPath('word-contract-mobile.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 1280, height: 720 });
  await expect.poll(async () => (await (await request.get(`/api/v1/documents/${doc.id}`, { headers })).json()).status, { timeout: 90_000 }).toBe('indexed');
  const original = await request.get(`/api/v1/documents/${doc.id}/file`, { headers });
  expect(await original.body()).toEqual(Buffer.from(source.content, 'base64'));

  const table = pdfTables.cases.find(c => c.name === 'merged-vertical')!;
  const tableUpload = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/documents');
  await page.getByLabel('Upload a contract', { exact: true }).setInputFiles({ name: 'native-table.pdf', mimeType: 'application/pdf', buffer: Buffer.from(table.content, 'base64') });
  const tableResponse = await tableUpload;
  expect(tableResponse.status()).toBe(200);
  const tableDoc = await tableResponse.json() as { id: string; ocr_required?: boolean };
  expect(tableDoc.ocr_required).not.toBe(true);
  await viewer.getByRole('button', { name: 'Text', exact: true }).click();
  await expect(viewer.getByRole('table')).toBeVisible();
  await expect(viewer.getByRole('columnheader')).toHaveText(['Term', 'Value']);
  await expect(viewer.getByRole('cell')).toHaveText(['Timing', '30 days', '', '60 days']);
  await page.screenshot({ path: testInfo.outputPath('pdf-table-desktop.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(viewer).toContainText('60 days');
  await page.screenshot({ path: testInfo.outputPath('pdf-table-mobile.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 1280, height: 720 });
  await expect.poll(async () => (await (await request.get(`/api/v1/documents/${tableDoc.id}`, { headers })).json()).status, { timeout: 90_000 }).toBe('indexed');
  const tableText = await request.get(`/api/v1/documents/${tableDoc.id}/content`, { headers });
  expect((await tableText.json()).text).toContain(table.tables[0]);
  const tableOriginal = await request.get(`/api/v1/documents/${tableDoc.id}/file`, { headers });
  expect(await tableOriginal.body()).toEqual(Buffer.from(table.content, 'base64'));
  const tableImage = await request.get(`/api/v1/documents/${tableDoc.id}/pages/0/image`, { headers });
  expect(tableImage.status()).toBe(200);
  expect(tableImage.headers()['content-type']).toMatch(/^image\//);
  await writeFile(testInfo.outputPath('pdf-table-goodmem.png'), await tableImage.body());
}
