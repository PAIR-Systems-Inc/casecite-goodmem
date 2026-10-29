import { expect, test } from '@playwright/test';

// No page.route mocks: this suite exercises the built Go image and GoodMem.
// scripts/integration.sh provisions disposable databases and a test provider.
test('original UI signs in, creates a matter, uploads through GoodMem, and retains its session', async ({ page, request }) => {
  test.setTimeout(180_000);
  const email = 'browser-owner@example.test';
  const password = 'Cedar!Trail7826';
  const csrf = await request.get('/api/v1/csrf-token');
  const csrfToken = (await csrf.json()).csrf_token as string;
  const registration = await request.post('/api/v1/auth/register', {
    headers: { 'X-CSRF-Token': csrfToken },
    data: { name: 'Browser Owner', email, password, bootstrap_token: 'integration-bootstrap' },
  });
  expect(registration.status(), await registration.text()).toBe(200);
  const access = (await registration.json()).access_token as string;
  const headers = { Authorization: `Bearer ${access}` };

  await page.goto('/matters');
  const header = page.locator('header[data-layout="header"]');
  await header.getByRole('button', { name: /Sign In/i }).click();
  const modal = page.locator('[class*="modalContainer"]');
  await modal.getByLabel('Email', { exact: true }).fill(email);
  await modal.getByLabel('Password', { exact: true }).fill(password);
  await modal.getByRole('button', { name: 'Sign In', exact: true }).click();
  await expect(header.getByText('browser-owner', { exact: true })).toBeVisible();

  const main = page.locator('main[data-layout="main-content"]');
  await expect(main.getByRole('heading', { name: 'Matters', exact: true })).toBeVisible();
  await main.getByRole('button', { name: 'New matter' }).click();
  await main.getByLabel('Matter name', { exact: true }).fill('Browser parity matter');
  await main.getByLabel('Client name', { exact: true }).fill('Acme Corp');
  await main.getByRole('button', { name: 'Create', exact: true }).click();
  await expect(main.getByText('Browser parity matter', { exact: true })).toBeVisible();
  await main.getByText('Browser parity matter', { exact: true }).click();
  await expect(main.getByRole('heading', { name: 'Browser parity matter' })).toBeVisible();
  await main.getByRole('tab', { name: 'Members' }).click();
  await expect(main.getByRole('listitem').filter({ hasText: email })).toContainText('Browser Owner');

  await page.goto('/documents');
  await expect(main.getByRole('heading', { name: 'Knowledge Base' })).toBeVisible();
  const uploadResponse = page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/documents');
  const source = 'Acme Corporation ("Provider") and Bravo LLC ("Customer").\n\nCustomer shall pay all invoices within thirty days.\n\nNotice is 30 days.';
  await page.getByLabel('Upload documents').setInputFiles({ name: 'browser-agreement.txt', mimeType: 'text/plain', buffer: Buffer.from(source) });
  const uploaded = await uploadResponse;
  expect(uploaded.status()).toBe(200);
  const document = await uploaded.json() as { id: string };
  await expect.poll(async () => {
    const response = await request.get(`/api/v1/documents/${document.id}`, { headers });
    expect(response.status()).toBe(200);
    return (await response.json()).status;
  }, { timeout: 90_000, intervals: [1000] }).toBe('indexed');
  // This must appear without reloading: upload completion refreshes the list.
  await expect(main.getByText('browser-agreement.txt', { exact: true })).toBeVisible({ timeout: 15_000 });
  const original = await request.get(`/api/v1/documents/${document.id}/file`, { headers });
  expect(original.status()).toBe(200);
  expect(await original.text()).toBe(source);
  const content = await request.get(`/api/v1/documents/${document.id}/content`, { headers });
  expect(content.status()).toBe(200);
  expect((await content.json()).text).toContain('Customer shall pay all invoices');

  await page.reload();
  await expect(header.getByText('browser-owner', { exact: true })).toBeVisible();
  await expect(main.getByText('browser-agreement.txt', { exact: true })).toBeVisible();
  const manifest = await request.get('/word-addin/manifest.xml');
  expect(manifest.status()).toBe(200);
  expect(await manifest.text()).toContain('/word-addin/');
  await header.getByRole('button', { name: 'Log out' }).click();
  await expect(header.getByRole('button', { name: /Sign In/i })).toBeVisible();
});
