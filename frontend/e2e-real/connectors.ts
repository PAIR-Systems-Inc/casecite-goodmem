import { expect, type APIRequestContext, type Page, type TestInfo } from '@playwright/test';
import { readFile } from 'node:fs/promises';

export async function exerciseConnectors(page: Page, request: APIRequestContext, headers: Record<string, string>, testInfo: TestInfo) {
  // Only the external consent page is simulated. The built Go server handles
  // the browser-bound callback, token exchange, listing, download and jobs.
  await page.context().route('http://provider:8080/oauth/authorize*', async route => {
    const params = new URL(route.request().url()).searchParams;
    expect(params.get('client_id')).toBe('fixture-clio-client');
    const callback = new URL(params.get('redirect_uri')!);
    callback.searchParams.set('state', params.get('state')!);
    callback.searchParams.set('code', 'fixture-authorized-code');
    await route.fulfill({ status: 302, headers: { Location: callback.href } });
  });
  await page.goto('/documents');
  const main = page.locator('main[data-layout="main-content"]');
  await page.locator('header[data-layout="header"]').getByRole('button', { name: /Settings/ }).click();
  const settings = page.getByRole('dialog', { name: 'Settings', exact: true });
  await settings.getByRole('button', { name: 'Sources', exact: true }).click();
  await settings.getByRole('button', { name: 'Set keys', exact: true }).first().click();
  await settings.getByLabel('Client ID', { exact: true }).fill('fixture-picker-client');
  await settings.getByLabel('API key (Picker)', { exact: true }).fill('fixture-browser-restricted-key');
  const saved = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/admin/integrations/connectors/google');
  await settings.getByRole('button', { name: 'Save & apply', exact: true }).click();
  expect((await saved).status()).toBe(200);
  expect(await (await request.get('/api/v1/pickers/config', { headers })).json()).toMatchObject({ google_enabled: true, google_client_id: 'fixture-picker-client' });
  await page.setViewportSize({ width: 390, height: 844 });
  const key = settings.getByLabel('API key (Picker)', { exact: true });
  await key.scrollIntoViewIfNeeded();
  const keyBounds = await key.boundingBox();
  expect(keyBounds!.x).toBeGreaterThanOrEqual(0);
  expect(keyBounds!.x + keyBounds!.width).toBeLessThanOrEqual(390);
  await page.screenshot({ path: testInfo.outputPath('connector-settings-mobile.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 1280, height: 720 });
  await settings.getByRole('button', { name: 'Close', exact: true }).first().click();
  await main.getByRole('button', { name: /Sources/ }).click();
  const google = main.getByTitle('Choose files from Google Drive', { exact: true });
  await expect(google).toContainText('Choose files');
  await google.click();
  const picker = page.getByRole('dialog', { name: 'Import from Google Drive', exact: true });
  await expect(picker.getByRole('button', { name: 'Browse Google Drive' })).toBeEnabled();
  await picker.getByRole('button', { name: 'Close', exact: true }).click();
  // Reset the public fixture key before the remainder of the UI walkthrough.
  expect((await request.delete('/api/v1/admin/integrations/connectors/google', { headers })).status()).toBe(200);
  await main.getByTitle('Connect Clio', { exact: true }).click();
  const manage = main.getByTitle('Manage Clio', { exact: true });
  await expect(manage).toBeVisible({ timeout: 15_000 });
  await manage.click();
  await page.getByRole('dialog', { name: 'Manage Clio', exact: true }).getByRole('button', { name: 'Sync Now', exact: true }).click();
  const startedResponse = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/connectors/clio/sync');
  await page.getByRole('dialog', { name: 'Import the entire Clio account?', exact: true }).getByRole('button', { name: 'Import entire account', exact: true }).click();
  const started = await startedResponse;
  expect(started.status(), await started.text()).toBe(200);
  const id = (await started.json()).sync_id;
  await expect.poll(async () => (await (await request.get(`/api/v1/connectors/clio/sync/${id}`, { headers })).json()).status, { timeout: 90_000, intervals: [1000] }).toBe('completed');
  const job = await (await request.get(`/api/v1/connectors/clio/sync/${id}`, { headers })).json();
  expect(job.result).toMatchObject({ docs_processed: 1, docs_skipped: 0, errors_count: 0 });
  // List refresh happens after the sync without reloading the browser.
  await expect(main.getByText('clio-scanned-notice.pdf', { exact: true })).toBeVisible({ timeout: 15_000 });
  const docs = await (await request.get('/api/v1/documents', { headers })).json();
  const doc = docs.documents.find((d: { source: string }) => d.source === 'clio');
  expect(doc).toMatchObject({ status: 'indexed', ocr_required: true, source_id: '100' });
  const content = await (await request.get(`/api/v1/documents/${doc.id}/content`, { headers })).json();
  expect(content.text).toContain('Notice is 30 days.');
  expect(await (await request.get(`/api/v1/documents/${doc.id}/file`, { headers })).body()).toEqual(await readFile('../server/internal/app/testdata/scanned_notice.pdf'));

  const again = await (await request.post('/api/v1/connectors/clio/sync', { headers, data: { sync_all: true } })).json();
  await expect.poll(async () => (await (await request.get(`/api/v1/connectors/clio/sync/${again.sync_id}`, { headers })).json()).status).toBe('completed');
  const repeated = await (await request.get(`/api/v1/connectors/clio/sync/${again.sync_id}`, { headers })).json();
  expect(repeated.result).toMatchObject({ docs_processed: 0, docs_skipped: 1 });
  await manage.scrollIntoViewIfNeeded();
  await page.screenshot({ path: testInfo.outputPath('connectors-desktop.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 390, height: 844 });
  await manage.scrollIntoViewIfNeeded();
  const bounds = await manage.boundingBox();
  expect(bounds!.x).toBeGreaterThanOrEqual(0);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(390);
  await page.screenshot({ path: testInfo.outputPath('connectors-mobile.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 1280, height: 720 });
  await manage.click();
  await page.getByRole('dialog', { name: 'Manage Clio', exact: true }).getByRole('button', { name: 'Disconnect', exact: true }).click();
  await page.getByRole('dialog', { name: 'Disconnect Clio', exact: true }).getByRole('button', { name: 'Confirm', exact: true }).click();
  await expect(main.getByTitle('Connect Clio', { exact: true })).toBeVisible();
  // Disconnect retains imported source records and their citations.
  expect((await request.get(`/api/v1/documents/${doc.id}`, { headers })).status()).toBe(200);
  await page.context().unroute('http://provider:8080/oauth/authorize*');
}
