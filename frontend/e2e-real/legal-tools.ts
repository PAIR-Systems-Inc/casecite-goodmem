import { expect, type Page, type TestInfo } from '@playwright/test';

// Real Go routes with a deterministic CourtListener HTTP provider. This proves
// UI/API integration; it does not claim a live CourtListener or audio-host test.
export async function exerciseLegalTools(page: Page, testInfo: TestInfo) {
  page.setDefaultTimeout(15_000);
  await expect(page.getByText('Research run deleted.', { exact: true })).toBeHidden({ timeout: 10_000 });
  const main = page.locator('main[data-layout="main-content"]');
  const sidebar = page.locator('[data-layout="left-sidebar"]');
  const search = async (name: string, query = 'notice') => {
    await sidebar.getByRole('button', { name: new RegExp(`^${name}`) }).click();
    await main.getByRole('heading', { name, exact: true }).waitFor();
    await main.getByLabel(`${name} search`, { exact: true }).fill(query);
    await main.getByRole('button', { name: `Search ${name}`, exact: true }).click();
  };

  await search('Case Lookup');
  await expect(main.getByText('2 cases found', { exact: true })).toBeVisible();
  await expect(main.getByText('100 U.S. 20 · 10 S. Ct. 2', { exact: true })).toBeVisible();
  await main.getByRole('button', { name: /Load more/i }).click();
  await expect(main.getByText('Showing 2', { exact: true })).toBeVisible();
  await expect(main.getByRole('button', { name: /Load more/i })).toBeHidden();
  await main.getByRole('button', { name: /^Notice v. Example/ }).first().click();
  await expect(page).toHaveURL(/\/case\/77\?kind=cluster$/);
  await expect(main.getByRole('heading', { name: 'Notice v. Example', exact: true })).toBeVisible();
  await expect(main.getByText('The notice provision applies.', { exact: true })).toBeVisible();
  await expect(main.getByRole('heading', { name: 'Dissent', exact: true })).toBeVisible();
  await expect(main.getByText('I respectfully dissent .', { exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('legal-case-desktop.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 390, height: 844 });
  const title = main.getByRole('heading', { name: 'Notice v. Example', exact: true });
  const titleBox = await title.boundingBox();
  expect(titleBox!.x).toBeGreaterThanOrEqual(0);
  expect(titleBox!.x + titleBox!.width).toBeLessThanOrEqual(390);
  await page.screenshot({ path: testInfo.outputPath('legal-case-mobile.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 1280, height: 720 });
  await main.getByRole('button', { name: 'Back to search', exact: true }).click();
  await expect(main.getByText('Showing 2', { exact: true })).toBeVisible();

  await search('Precedents');
  await main.getByRole('button', { name: /Notice v. Example/ }).click();
  await expect(main.getByText('I respectfully dissent .', { exact: true })).toBeVisible();
  await main.getByRole('button', { name: 'Back to results', exact: true }).click();
  await expect(main.getByText('1 cases found · most cited first', { exact: true })).toBeVisible();

  await search('Dockets');
  await main.getByRole('button', { name: /Notice v. Example/ }).click();
  await expect(main.getByText(/Represented by Attorney One/)).toBeVisible();
  await expect(main.getByText('Motion', { exact: true })).toBeVisible();
  await expect(main.getByRole('link', { name: 'Motion (4 pp.)', exact: true })).toHaveAttribute('href', 'https://www.courtlistener.com/recap/8/motion.pdf');
  await page.screenshot({ path: testInfo.outputPath('legal-docket-desktop.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: testInfo.outputPath('legal-docket-mobile.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 1280, height: 720 });

  await search('Oral Arguments');
  await expect(main.getByText(/Court of Appeals.*1:01:01/)).toBeVisible();
  await expect(main.locator('audio')).toHaveAttribute('controls', '');
  await expect(main.locator('audio source')).toHaveAttribute('src', 'https://storage.courtlistener.com/audio/2020/notice.mp3');

  await search('Citation Check', '100 U.S. 20');
  await expect(main.getByText('No negative language found', { exact: true })).toBeVisible();
  await expect(main.getByText('Later v. Notice', { exact: true })).toBeVisible();
  await expect(main.getByText('Positive language (1)', { exact: true })).toBeVisible();

  await search('Trends');
  await expect(main.getByText('Peak · 160 cases', { exact: true })).toBeVisible({ timeout: 20_000 });
  await page.screenshot({ path: testInfo.outputPath('legal-trends-desktop.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 390, height: 844 });
  const input = main.getByLabel('New Trends search', { exact: true });
  // Resizing animates both desktop sidebars off-screen before their mobile
  // drawer state settles. Check the stable layout, not that transition frame.
  await expect(sidebar).not.toBeInViewport();
  await expect(page.locator('[data-layout="right-panel"]')).not.toBeInViewport();
  const inputBox = await input.boundingBox();
  expect(inputBox!.width).toBeGreaterThan(150);
  expect(inputBox!.x + inputBox!.width).toBeLessThanOrEqual(390);
  const searchButton = main.getByRole('button', { name: 'Search Trends', exact: true });
  await searchButton.click({ trial: true });
  const searchBox = await searchButton.boundingBox();
  expect(searchBox!.y + searchBox!.height).toBeLessThanOrEqual(776);
  expect(await searchButton.evaluate((button) => {
    const r = button.getBoundingClientRect();
    const top = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
    return top === button || button.contains(top);
  })).toBe(true);
  await page.screenshot({ path: testInfo.outputPath('legal-trends-mobile.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 1280, height: 720 });

  await search('Case Lookup', 'upstream-failure');
  await expect(main.getByText('CourtListener returned HTTP 503. Please try again.', { exact: true })).toBeVisible({ timeout: 10_000 });
  await main.getByLabel('New Case Lookup search', { exact: true }).fill('notice');
  await main.getByLabel('New Case Lookup search', { exact: true }).press('Enter');
  await expect(main.getByText('2 cases found', { exact: true })).toBeVisible();
}
