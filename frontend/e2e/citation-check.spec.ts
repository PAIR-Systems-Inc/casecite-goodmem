import { test, expect, type Page } from '@playwright/test';
import { loginAs, fulfillJson, type MockOverride } from './helpers/auth';

// The one notice every generated artifact carries (src/components/shared/AiNotice.tsx).
const AI_NOTICE_TEXT =
  'AI-generated. Verify every statement, citation and clause against the source documents and current law before relying on it.';

const CITATION = '410 U.S. 113';

// GET /tools/validate-citation response, as CitationValidationResult reads it.
const validationResult = {
  citation: CITATION,
  case_name: 'Roe v. Wade',
  absolute_url: 'https://www.courtlistener.com/opinion/108713/roe-v-wade/',
  warning_level: 'warning',
  total_citing_cases: 4321,
  citing_cases_analyzed: 40,
  positive_citations: 12,
  caution_citations: 3,
  negative_citations: 2,
  last_cited: '2024-05-01',
  citing_cases: [
    {
      id: 1, case_name: 'Dobbs v. Jackson Women\'s Health Organization', citation: '597 U.S. 215',
      court: 'Supreme Court of the United States', date_filed: '2022-06-24', treatment: 'negative',
      snippet: 'Roe was egregiously wrong from the start.', context_found: true,
      url: 'https://www.courtlistener.com/opinion/dobbs/',
    },
  ],
  notes: ['Scanned 40 citing opinions for treatment language.'],
};

const validateCitation = (status: number, body: unknown): MockOverride => async (route, url, method) => {
  if (method !== 'GET' || !url.pathname.endsWith('/tools/validate-citation')) return false;
  await fulfillJson(route, status, body);
  return true;
};

/** From /tools, open Citation Check from the sidebar and run one check. */
async function runCitationCheck(page: Page) {
  await expect(page.getByRole('heading', { name: 'Legal Tools' })).toBeVisible();
  await page.locator('aside[data-layout="left-sidebar"]').getByRole('button', { name: /Citation Check/ }).click();
  await expect(page).toHaveURL(/\/tools\/validate-citation$/);
  await expect(page.getByRole('heading', { name: 'Citation Check' })).toBeVisible();

  const request = page.waitForRequest((req) => req.url().includes('/tools/validate-citation'));
  await page.getByLabel('Citation Check search').fill(CITATION);
  await page.keyboard.press('Enter');
  return request;
}

test.describe('Citation check', () => {
  test.use({ viewport: { width: 1280, height: 1000 } });

  test('renders the treatment result with the AI-review notice', async ({ page }) => {
    await loginAs(page, '/tools', { overrides: [validateCitation(200, validationResult)] });
    const request = await runCitationCheck(page);
    expect(new URL(request.url()).searchParams.get('citation')).toBe(CITATION);

    const main = page.locator('main[data-layout="main-content"]');
    const card = main.locator('[class*="resultCard"]');
    await expect(card).toBeVisible({ timeout: 10_000 });
    await expect(card).toContainText('Citation check · CourtListener');
    await expect(card).toContainText('Roe v. Wade');
    await expect(card).toContainText(CITATION);
    await expect(card).toContainText('Negative language found — read before relying');
    await expect(card).toContainText('in 40 recent and most-cited citing opinions');
    await expect(card).toContainText('4,321');
    await expect(card).toContainText('Negative language (1)');
    await expect(card).toContainText('Dobbs v. Jackson Women\'s Health Organization');
    await expect(card).toContainText('Roe was egregiously wrong from the start.');
    await expect(card).toContainText('Scanned 40 citing opinions for treatment language.');
    await expect(card.getByRole('link', { name: /Open case on CourtListener/ }))
      .toHaveAttribute('href', validationResult.absolute_url);

    // Every generated artifact ends with the AI-review notice.
    await expect(card.getByRole('note')).toHaveText(AI_NOTICE_TEXT);

    // No error, and the composer offers result-chat now that a result is up.
    await expect(main.locator('[class*="toolErrorBox"]')).toHaveCount(0);
    await expect(main.getByRole('button', { name: /Chat with this/ })).toBeVisible();
  });

  test('a 502 from the backend shows the error and no result', async ({ page }) => {
    await loginAs(page, '/tools', {
      overrides: [validateCitation(502, { detail: 'CourtListener is unavailable. Try again shortly.' })],
    });
    await runCitationCheck(page);

    const main = page.locator('main[data-layout="main-content"]');
    const errorBox = main.locator('[class*="toolErrorBox"]');
    await expect(errorBox).toBeVisible({ timeout: 10_000 });
    await expect(errorBox).toContainText('CourtListener is unavailable. Try again shortly.');

    await expect(main.locator('[class*="resultCard"]')).toHaveCount(0);
    await expect(main.getByRole('note')).toHaveCount(0);
    // The session stays signed in — a bad upstream is not an auth failure.
    await expect(page.locator('header[data-layout="header"]').getByRole('button', { name: 'Log out' })).toBeVisible();
    // A new search is still offered from the bottom composer.
    await expect(main.getByLabel('New Citation Check search')).toBeVisible();
  });
});
