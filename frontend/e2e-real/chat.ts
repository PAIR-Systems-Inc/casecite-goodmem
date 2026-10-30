import { expect, type APIRequestContext, type Page, type TestInfo } from '@playwright/test';

export async function exerciseChat(page: Page, request: APIRequestContext, headers: Record<string, string>, testInfo: TestInfo) {
  const main = page.locator('main[data-layout="main-content"]');
  await page.goto('/research');
  await main.getByLabel('Search matters and files').fill('scanned-notice');
  const expand = main.getByRole('button', { name: /Show files in/ });
  for (let count = 0; count < 5 && await expand.count(); count++) await expand.first().click();
  await main.getByRole('button', { name: 'Add scanned-notice.pdf to selection', exact: true }).click();
  const queued = page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/chat');
  await main.getByRole('textbox', { name: 'Ask or strategize across your documents' }).fill('What notice does the scanned agreement require?');
  await main.getByRole('button', { name: 'Send question' }).click();
  const response = await queued;
  expect(response.status(), await response.text()).toBe(202);
  const job = await response.json() as { job_id: string };
  await expect.poll(async () => (await (await request.get(`/api/v1/jobs/${job.job_id}`, { headers })).json()).status, { timeout: 30_000 }).toBe('completed');
  const completed = await (await request.get(`/api/v1/jobs/${job.job_id}`, { headers })).json();
  const answer = completed.result;
  expect(answer.citations).toHaveLength(1);
  expect(answer.citations[0]).toMatchObject({ source: 'scanned-notice.pdf', verified: true, support: 'supported', page_start: 1 });
  expect(answer.citations[0].doc_span_end).toBeGreaterThan(answer.citations[0].doc_span_start);
  const assistant = main.locator('[class*="assistantBubble"]').last();
  await expect(assistant).toContainText('The agreement requires 30 days of notice.');
  await expect(main.locator('[class*="processingBubble"]')).toHaveCount(0);
  for (const [label, extension] of [['Word', 'docx'], ['PDF', 'pdf'], ['Markdown', 'md']]) {
    await main.getByRole('button', { name: 'Export', exact: true }).click();
    const downloaded = page.waitForEvent('download');
    await main.getByRole('menuitem', { name: new RegExp(label) }).click();
    const download = await downloaded;
    expect(download.suggestedFilename()).toMatch(new RegExp(`\\.${extension}$`));
    await download.saveAs(testInfo.outputPath(`chat.${extension}`));
  }
  const persisted = await request.get(`/api/v1/chat/sessions/${answer.session_id}`, { headers });
  expect(persisted.status()).toBe(200);
  expect(await persisted.json()).toMatchObject({ message_count: 2, messages: [{ role: 'user' }, { id: answer.id, role: 'assistant' }] });

  await main.getByRole('textbox', { name: 'Ask or strategize across your documents' }).fill('Make a strategy for this agreement');
  await main.getByRole('button', { name: 'Send question' }).click();
  const brief = main.locator('[class*="briefCard"]');
  await expect(brief).toBeVisible({ timeout: 30_000 });
  await expect(brief).toContainText('1 document reviewed');
  await expect(brief).toContainText('No supporting document passage was identified for this point.');
  await brief.scrollIntoViewIfNeeded();
  await page.screenshot({ path: testInfo.outputPath('chat-strategy-desktop.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 390, height: 844 });
  await brief.scrollIntoViewIfNeeded();
  await expect(brief).toBeVisible();
  const box = await brief.boundingBox();
  expect(box!.x).toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width).toBeLessThanOrEqual(390);
  await page.screenshot({ path: testInfo.outputPath('chat-strategy-mobile.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 1280, height: 720 });

  const cancelQueued = page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/chat');
  await main.getByRole('textbox', { name: 'Ask or strategize across your documents' }).fill('Pause this answer for the cancellation test');
  await main.getByRole('button', { name: 'Send question' }).click();
  const cancelledJob = await (await cancelQueued).json() as { job_id: string };
  await main.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect.poll(async () => (await (await request.get(`/api/v1/jobs/${cancelledJob.job_id}`, { headers })).json()).status).toBe('cancelled');
  const session = await request.get(`/api/v1/chat/sessions/${answer.session_id}`, { headers });
  expect((await session.json()).message_count).toBe(4);
}
