import { test, expect } from '@playwright/test';
import { loginAs, fulfillJson, type MockDocument } from './helpers/auth';

// Upload through the sidebar's file input (DocumentPanel) with the backend
// mocked. Indexing is synchronous server-side — POST /documents answers with
// the finished document, and the app then re-fetches GET /documents for the
// Knowledge Base list and the header count; there is no job to poll.
const FILE_NAME = 'smith-v-jones-brief.txt';
const FILE_BODY = Buffer.from('IN THE COURT OF APPEALS\n\nBrief of Appellant, Smith v. Jones.\n');

const indexedDoc: MockDocument = {
  id: 'doc-42',
  filename: FILE_NAME,
  content_type: 'text/plain',
  size: FILE_BODY.length,
  source: 'upload',
  status: 'indexed',
  chunk_count: 3,
  created_at: '2026-10-28T09:00:00Z',
  folder_path: null,
};

test.describe('Document upload', () => {
  test.use({ viewport: { width: 1280, height: 1000 } });

  test('an uploaded file is indexed and appears in the Knowledge Base', async ({ page }) => {
    const state = await loginAs(page, '/documents', {
      overrides: [
        async (route, url, method, st) => {
          if (method !== 'POST' || !url.pathname.endsWith('/documents')) return false;
          // The real response: the stored document (DocumentInfo + RagDocument fields).
          st.documents.push(indexedDoc);
          await fulfillJson(route, 200, { ...indexedDoc, document_id: indexedDoc.id, name: FILE_NAME });
          return true;
        },
      ],
    });

    await expect(page.getByRole('heading', { name: 'Knowledge Base' })).toBeVisible();
    await expect(page.getByText('No documents yet')).toBeVisible();

    const uploadRequest = page.waitForRequest(
      (req) => req.method() === 'POST' && /\/api\/v1\/documents$/.test(req.url()),
    );
    await page.getByLabel('Upload documents').setInputFiles({
      name: FILE_NAME, mimeType: 'text/plain', buffer: FILE_BODY,
    });

    // Multipart body carrying the file (the client must not JSON-encode it).
    const upload = await uploadRequest;
    expect(upload.headers()['content-type']).toMatch(/^multipart\/form-data/);
    expect(upload.postData()).toContain(`filename="${FILE_NAME}"`);

    // The sidebar chip confirms the index (it lingers ~2.5s, so assert promptly) ...
    const sidebar = page.locator('aside[data-layout="left-sidebar"]');
    await expect(sidebar.getByText('Upload successful · 3 chunks')).toBeVisible({ timeout: 5000 });

    // ... and the Knowledge Base list and header count refresh from GET /documents.
    const main = page.locator('main[data-layout="main-content"]');
    await expect(main.getByText(FILE_NAME)).toBeVisible();
    await expect(main.getByText('3 chunks', { exact: false })).toBeVisible();
    await expect(main.getByText('No documents yet')).toHaveCount(0);
    await expect(page.locator('header[data-layout="header"]')).toContainText('1 documents indexed');

    expect(state.calls.filter((c) => c === 'GET /documents').length).toBeGreaterThanOrEqual(2);
  });

  test('a rejected upload shows the server reason and stays dismissible', async ({ page }) => {
    await loginAs(page, '/documents', {
      overrides: [
        async (route, url, method) => {
          if (method !== 'POST' || !url.pathname.endsWith('/documents')) return false;
          await fulfillJson(route, 415, { detail: 'Unsupported file type: .exe' });
          return true;
        },
      ],
    });
    await expect(page.getByRole('heading', { name: 'Knowledge Base' })).toBeVisible();

    await page.getByLabel('Upload documents').setInputFiles({
      name: 'installer.txt', mimeType: 'text/plain', buffer: Buffer.from('MZ'),
    });

    const sidebar = page.locator('aside[data-layout="left-sidebar"]');
    await expect(sidebar.getByText('Unsupported file type: .exe')).toBeVisible({ timeout: 5000 });
    await expect(sidebar.getByText('installer.txt')).toBeVisible();

    // Nothing reached the list, and the failure chip can be dismissed.
    const main = page.locator('main[data-layout="main-content"]');
    await expect(main.getByText('No documents yet')).toBeVisible();
    await sidebar.getByRole('button', { name: 'Dismiss' }).click();
    await expect(sidebar.getByText('installer.txt')).toHaveCount(0);
  });
});
