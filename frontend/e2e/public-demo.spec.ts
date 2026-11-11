import { expect, test } from '@playwright/test';

test('public visitors can ask without an account and cannot upload', async ({ page }) => {
  await page.route('**/', async (route) => {
    const response = await route.fetch();
    await route.fulfill({ response, body: (await response.text()).replace('</head>', '<meta name="casecite-public-demo" content="true"></head>') });
  });
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith('/demo/session')) return route.fulfill({ status: 401, json: { detail: 'Open an invitation' } });
    if (path.endsWith('/demo/public/documents')) return route.fulfill({ json: [{ id: 'public', filename: 'sample-agreement.txt', status: 'indexed' }] });
    if (path.endsWith('/demo/public/chat')) return route.fulfill({ json: { answer: 'The notice period is 30 days. [1]', sources: [{ number: 1, filename: 'sample-agreement.txt', text: 'Either party may terminate with 30 days of notice.' }] } });
    return route.fulfill({ json: {} });
  });
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'CaseCite', exact: true })).toBeVisible();
  await expect(page.locator('input[type="file"]')).toHaveCount(0);
  await expect(page.locator('input[type="password"]')).toHaveCount(0);
  await page.getByLabel('Your question').fill('What is the notice period?');
  await page.getByRole('button', { name: 'Ask CaseCite' }).click();
  await expect(page.getByText('The notice period is 30 days. [1]')).toBeVisible();
  await page.getByText('[1] sample-agreement.txt', { exact: true }).click();
  await expect(page.getByText('Either party may terminate with 30 days of notice.')).toBeVisible();
  await page.screenshot({ path: 'test-results/public-demo-desktop.png', fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole('button', { name: 'Ask CaseCite' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: 'test-results/public-demo-mobile.png', fullPage: true });
});

test('invitation opens a private collection and is removed from the URL', async ({ page }) => {
  let redeemed = false;
  let used = 9;
  await page.route('**/', async (route) => {
    const response = await route.fetch();
    await route.fulfill({ response, body: (await response.text()).replace('</head>', '<meta name="casecite-public-demo" content="true"></head>') });
  });
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith('/demo/redeem')) { redeemed = true; return route.fulfill({ json: { private: true } }); }
    if (path.endsWith('/demo/session')) return route.fulfill({ json: { private: true, uploads_used: used, upload_limit: 10, expires_at: '2027-01-01T00:00:00Z' } });
    if (path.endsWith('/demo/private/documents')) {
      if (route.request().method() === 'POST') { used++; return route.fulfill({ json: { id: 'new', filename: 'private.txt', status: 'processing' } }); }
      return route.fulfill({ json: [{ id: 'private', filename: 'private-agreement.txt', status: 'indexed' }] });
    }
    return route.fulfill({ json: {} });
  });
  await page.goto('/#invite=test-token');
  await expect(page.getByRole('heading', { name: 'My private documents' })).toBeVisible();
  expect(redeemed).toBe(true);
  expect(new URL(page.url()).hash).toBe('');
  await page.locator('input[type="file"]').setInputFiles({ name: 'private.txt', mimeType: 'text/plain', buffer: Buffer.from('Private example') });
  await expect(page.getByText('10 of 10 uploads used.')).toBeVisible();
  await expect(page.locator('input[type="file"]')).toBeDisabled();
});
