import { expect, type Page, type TestInfo } from '@playwright/test';

export async function exerciseJudgeIntelligence(page: Page, testInfo: TestInfo) {
  const main = page.locator('main[data-layout="main-content"]');
  await page.locator('[data-layout="left-sidebar"]').getByRole('button', { name: /^Judge Intel/ }).click();
  await main.getByLabel('Judge name', { exact: true }).fill('Jane Example');
  await main.getByRole('button', { name: 'Search judges', exact: true }).click();
  await expect(main.getByText('Showing 1 judges', { exact: true })).toBeVisible();
  await main.getByRole('button', { name: /Jane Example/ }).click();
  await expect(main.getByRole('heading', { name: /Jane Example/ })).toBeVisible({ timeout: 30_000 });
  await expect(main.getByText('Example University', { exact: true }).first()).toBeVisible();
  await expect(main.getByRole('heading', { name: 'Statistics', exact: true })).toBeVisible();
  await expect(main.getByText(/Separate-writing and writing metrics use explicitly identified authors only/)).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('judge-desktop.png'), fullPage: true, animations: 'disabled' });
  await main.getByRole('button', { name: 'Most Cited', exact: true }).click();
  await main.getByRole('button', { name: /Notice v. Example/ }).click();
  await expect(main.getByText(/The notice provision applies/).last()).toBeVisible();
  await expect(main.getByText(/I respectfully dissent/).last()).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator('[data-layout="left-sidebar"]')).not.toBeInViewport();
  await expect(page.locator('[data-layout="right-panel"]')).not.toBeInViewport();
  await page.screenshot({ path: testInfo.outputPath('judge-mobile.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 1280, height: 720 });
  await main.getByRole('button', { name: 'Back to Search', exact: true }).click();
  const cached = page.waitForResponse((r) => r.url().includes('/judge-intel/build/42') && r.request().method() === 'POST');
  await main.getByRole('button', { name: /Jane Example/ }).click();
  expect((await (await cached).json()).status).toBe('cached');
  await expect(main.getByRole('heading', { name: /Jane Example/ })).toBeVisible();
}
