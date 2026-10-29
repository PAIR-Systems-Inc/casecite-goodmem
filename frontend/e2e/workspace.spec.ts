import { test, expect } from '@playwright/test';

function samplePDF() {
  const stream = 'BT /F1 14 Tf 60 740 Td (Notice is 30 days.) Tj 0 -24 Td (Payment is due monthly.) Tj ET';
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>',
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
    `<< /Length ${stream.length} >>\nstream\n${stream}\nendstream`,
  ];
  let pdf = '%PDF-1.4\n';
  const offsets = [0];
  objects.forEach((o, i) => {
    offsets.push(pdf.length);
    pdf += `${i + 1} 0 obj\n${o}\nendobj\n`;
  });
  const start = pdf.length;
  pdf += `xref\n0 6\n0000000000 65535 f \n`;
  offsets.slice(1).forEach((n) => (pdf += `${String(n).padStart(10, '0')} 00000 n \n`));
  return Buffer.from(pdf + `trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n${start}\n%%EOF\n`);
}

test('real GoodMem workspace: upload, pages, research, review, export, history and sessions', async ({ page }) => {
  const browserErrors: string[] = [];
  page.on('pageerror', (e) => browserErrors.push(e.message));
  await page.goto('/');
  await page.getByLabel('Name', { exact: true }).fill('Test Attorney');
  await page.getByLabel('Email', { exact: true }).fill('attorney@example.test');
  await page.getByLabel('Password', { exact: true }).fill('integration-password-123');
  await page.getByLabel('Installation setup token').fill('integration-bootstrap');
  await page.getByRole('button', { name: 'Create workspace' }).click();
  await expect(page.getByRole('heading', { name: 'Research', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'New matter', exact: true }).click();
  await page.getByLabel('Matter name').fill('Acme contract review');
  await page.getByLabel('Client name').fill('Acme');
  await page.getByRole('button', { name: 'Create matter', exact: true }).click();
  await expect(page.getByRole('dialog')).not.toBeVisible();
  await page.getByRole('button', { name: 'Document library', exact: true }).click();
  await page
    .locator('input[type=file]')
    .setInputFiles({ name: 'Notice agreement.pdf', mimeType: 'application/pdf', buffer: samplePDF() });
  await expect(page.locator('.badge.good')).toHaveText('indexed', { timeout: 60000 });
  await page
    .getByRole('button', { name: /Notice agreement.pdf/ })
    .first()
    .click();
  await expect(page.getByRole('img', { name: 'Page 1 of Notice agreement.pdf' })).toBeVisible();
  await expect
    .poll(() =>
      page
        .getByRole('img', { name: 'Page 1 of Notice agreement.pdf' })
        .evaluate((img: HTMLImageElement) => img.naturalWidth),
    )
    .toBeGreaterThan(100);
  await page.getByRole('button', { name: 'Text', exact: true }).click();
  await expect(page.locator('.document-text')).toContainText('Notice is 30 days.');
  await page.getByRole('button', { name: 'Close', exact: true }).click();
  await page.getByRole('button', { name: 'Research', exact: true }).click();
  await page.getByRole('button', { name: 'Select all' }).click();
  await page.getByLabel('Instructions').fill('What is the notice period?');
  await page.locator('.composer').getByRole('button', { name: 'Research', exact: true }).click();
  await expect(page.locator('.result .prose')).toContainText('30 days', { timeout: 60000 });
  await expect(page.locator('.citations')).toContainText('Notice agreement.pdf');
  await page.screenshot({ path: 'test-results/research-desktop.png', fullPage: true });
  await page.getByRole('button', { name: 'Contract review', exact: true }).click();
  await page.getByRole('button', { name: 'Run review', exact: true }).click();
  await expect(page.getByText('Exact quote matched')).toBeVisible({ timeout: 60000 });
  const downloadPromise = page.waitForEvent('download');
  await page.getByRole('link', { name: 'Word', exact: true }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe('casecite-result.docx');
  await page.getByRole('button', { name: 'Review tables', exact: true }).click();
  await page.getByLabel('Review fields').fill('Notice');
  await page.getByRole('button', { name: 'Run review', exact: true }).click();
  await expect(page.locator('.review-cell')).toContainText('30 days', { timeout: 60000 });
  await page.locator('.review-cell select').selectOption('approved');
  await expect(page.locator('.review-cell select')).toHaveValue('approved');
  await page.getByRole('button', { name: 'Saved work', exact: true }).click();
  await expect(page.locator('.history-card')).toHaveCount(3);
  await page.locator('.history-card').filter({ hasText: 'Review tables' }).click();
  await expect(page.locator('.review-cell select')).toHaveValue('approved');
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Provider keys' })).toBeVisible();
  await page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Sign in to CaseCite' })).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByLabel('Email', { exact: true }).fill('attorney@example.test');
  await page.getByLabel('Password', { exact: true }).fill('integration-password-123');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Research', exact: true })).toBeVisible();
  await page.screenshot({ path: 'test-results/research-mobile.png', fullPage: true });
  expect(browserErrors).toEqual([]);
});
