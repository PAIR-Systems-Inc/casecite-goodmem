import { expect, test } from '@playwright/test';

// No page.route mocks: this suite exercises the built Go image and GoodMem.
// scripts/integration.sh provisions disposable databases and a test provider.
test('original UI manages matters, GoodMem documents and cited tabular review', async ({ page, request }, testInfo) => {
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

  await page.goto('/review-tables');
  await expect(main.getByRole('heading', { name: 'Tabular Review' })).toBeVisible();
  await main.getByRole('button', { name: /Blank.*define your own fields/ }).click();
  await main.getByLabel('Review name', { exact: true }).fill('Browser diligence');
  await main.getByRole('button', { name: 'Add field', exact: true }).click();
  await main.getByLabel('Field 1 name', { exact: true }).fill('Notice');
  await main.getByLabel('Field 1 type', { exact: true }).selectOption('duration');
  await main.getByRole('button', { name: 'Add field', exact: true }).click();
  await main.getByLabel('Field 2 name', { exact: true }).fill('Liability cap');
  await main.getByLabel('Field 2 type', { exact: true }).selectOption('money');
  await main.getByRole('button', { name: 'Save as template', exact: true }).click();
  await main.getByLabel('Template name', { exact: true }).fill('Browser template');
  const saveTemplate = page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/review-tables/schemas');
  await main.getByRole('group', { name: 'Save review template' }).getByRole('button', { name: 'Save', exact: true }).click();
  expect((await saveTemplate).status()).toBe(201);
  await expect(main.getByRole('button', { name: 'Create review', exact: true })).toBeVisible();
  const createReview = page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/review-tables');
  await main.getByRole('button', { name: 'Create review', exact: true }).click();
  const tableResponse = await createReview;
  expect(tableResponse.status()).toBe(201);
  const table = await tableResponse.json() as { id: string };
  await expect(main.getByRole('heading', { name: 'Browser diligence' })).toBeVisible();
  await main.getByRole('button', { name: 'Documents', exact: true }).click();
  const picker = page.getByRole('dialog', { name: 'Choose documents' });
  await picker.getByRole('checkbox', { name: /browser-agreement.txt/ }).check();
  await picker.getByRole('button', { name: 'Use 1 document', exact: true }).click();
  await main.getByRole('button', { name: 'Run review', exact: true }).click();
  const grid = main.getByRole('grid', { name: 'Review results' });
  await expect(grid.getByRole('gridcell').filter({ hasText: '30 days' })).toBeVisible({ timeout: 30_000 });
  await expect(main.getByRole('button', { name: 'Run review', exact: true })).toBeEnabled();
  await grid.getByRole('gridcell').filter({ hasText: 'USD 1 million' }).click();
  const sourcePanel = main.getByRole('complementary', { name: 'Cell source and review' });
  await expect(sourcePanel.getByText('Unverified: the quote was not found in the document')).toBeVisible();
  await grid.getByRole('gridcell').filter({ hasText: '30 days' }).click();
  await expect(sourcePanel.getByText('Verified against the document')).toBeVisible();
  await expect(sourcePanel.locator('mark')).toContainText('Notice is 30 days.');
  await sourcePanel.getByRole('button', { name: /Click to override/ }).click();
  await sourcePanel.getByLabel('Override value').fill('45 days');
  await sourcePanel.getByRole('button', { name: 'Save', exact: true }).click();
  await sourcePanel.getByRole('button', { name: 'Approve', exact: true }).click();
  await sourcePanel.getByLabel('Review note').fill('Counsel confirmed the amendment.');
  await sourcePanel.getByLabel('Review note').blur();
  await expect(sourcePanel.getByRole('button', { name: 'Approved', exact: true })).toBeVisible();
  const rerunResponse = page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === `/api/v1/review-tables/${table.id}/run`);
  await sourcePanel.getByRole('button', { name: 'Re-run', exact: true }).click();
  const rerun = await (await rerunResponse).json() as { job_id: string };
  await expect.poll(async () => (await (await request.get(`/api/v1/jobs/${rerun.job_id}`, { headers })).json()).status, { timeout: 30_000 }).toBe('completed');
  await expect(main.getByRole('button', { name: 'Run review', exact: true })).toBeEnabled({ timeout: 30_000 });
  const saved = await request.get(`/api/v1/review-tables/${table.id}`, { headers });
  const savedCell = (await saved.json()).cells.find((c: { field_key: string }) => c.field_key === 'notice');
  expect(savedCell).toMatchObject({ value: '30 days', effective_value: '45 days', review_status: 'approved', note: 'Counsel confirmed the amendment.' });
  await main.getByRole('button', { name: 'Runs', exact: true }).click();
  await expect(main.getByRole('region', { name: 'Run history' }).getByRole('listitem')).toHaveCount(2);
  await main.getByRole('button', { name: 'Export', exact: true }).click();
  const downloadPromise = page.waitForEvent('download');
  await main.getByRole('dialog', { name: 'Export options' }).getByRole('button', { name: 'Download XLSX' }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toMatch(/\.xlsx$/);
  await download.saveAs(testInfo.outputPath('review.xlsx'));
  await expect(page.getByText('Exported XLSX.', { exact: true })).toBeHidden({ timeout: 10_000 });
  await page.screenshot({ path: testInfo.outputPath('review-desktop.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator('[data-layout="left-sidebar"]')).not.toBeInViewport();
  await expect(main.getByRole('heading', { name: 'Browser diligence' })).toBeVisible();
  await main.getByRole('heading', { name: 'Browser diligence' }).scrollIntoViewIfNeeded();
  const headingBox = await main.getByRole('heading', { name: 'Browser diligence' }).boundingBox();
  expect(headingBox).not.toBeNull();
  expect(headingBox!.width).toBeGreaterThan(150);
  expect(headingBox!.x + headingBox!.width).toBeLessThanOrEqual(390);
  await main.getByRole('button', { name: 'Export', exact: true }).click();
  const mobileExport = main.getByRole('dialog', { name: 'Export options' });
  await expect(mobileExport).toBeVisible();
  const exportBox = await mobileExport.boundingBox();
  expect(exportBox!.x).toBeGreaterThanOrEqual(0);
  expect(exportBox!.x + exportBox!.width).toBeLessThanOrEqual(390);
  await page.keyboard.press('Escape');
  await page.screenshot({ path: testInfo.outputPath('review-mobile.png'), fullPage: true, animations: 'disabled' });
  await sourcePanel.scrollIntoViewIfNeeded();
  const sourceBox = await sourcePanel.boundingBox();
  expect(sourceBox!.x).toBeGreaterThanOrEqual(0);
  expect(sourceBox!.x + sourceBox!.width).toBeLessThanOrEqual(390);
  await expect(sourcePanel.getByRole('button', { name: 'Approved', exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('review-source-mobile.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 1280, height: 720 });

  const manifest = await request.get('/word-addin/manifest.xml');
  expect(manifest.status()).toBe(200);
  expect(await manifest.text()).toContain('/word-addin/');
  await header.getByRole('button', { name: 'Log out' }).click();
  await expect(header.getByRole('button', { name: /Sign In/i })).toBeVisible();
});
