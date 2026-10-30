import { expect, type APIRequestContext, type Page, type TestInfo } from '@playwright/test';

export async function exerciseProviderSettings(page: Page, request: APIRequestContext, headers: Record<string, string>, testInfo: TestInfo) {
  const path = '/api/v1/admin/integrations/local-llm';
  const before = await (await request.get(path, { headers })).json();
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.locator('header[data-layout="header"]').getByRole('button', { name: /Settings/ }).click();
  const settings = page.getByRole('dialog', { name: 'Settings', exact: true });
  await settings.getByRole('button', { name: 'API Keys', exact: true }).click();
  await expect(settings.getByLabel('Base URL', { exact: true })).toHaveValue(before.base_url);
  const saved = page.waitForResponse((response) => response.request().method() === 'POST' && new URL(response.url()).pathname === path);
  await settings.getByLabel('Chat Model', { exact: true }).fill('test-model-admin');
  expect((await saved).status()).toBe(200);
  const effective = await (await request.get('/api/v1/settings/rag', { headers })).json();
  expect(effective.llm_model).toBe('test-model-admin');

  // The test deployment has no generation API key. A real GoodMem search and
  // queued generation still use the administrator's local model immediately.
  const documents = await (await request.get('/api/v1/documents', { headers })).json();
  const started = await request.post('/api/v1/jobs', { headers, data: { kind: 'research', prompt: 'What notice is required?', document_ids: [documents.documents[0].id] } });
  expect(started.status(), await started.text()).toBe(202);
  const job = (await started.json()).job_id;
  await expect.poll(async () => (await (await request.get(`/api/v1/jobs/${job}`, { headers })).json()).status, { timeout: 30_000 }).toBe('completed');
  const result = await (await request.get(`/api/v1/jobs/${job}`, { headers })).json();
  expect(result.result.stats).toMatchObject({ model: 'test-model-admin', provider: 'self_hosted' });

  await settings.getByLabel('Base URL', { exact: true }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: testInfo.outputPath('provider-settings-desktop.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 390, height: 844 });
  const reset = settings.getByRole('button', { name: 'Revert to deployment defaults', exact: true });
  await reset.scrollIntoViewIfNeeded();
  const bounds = await reset.boundingBox();
  expect(bounds!.x).toBeGreaterThanOrEqual(0);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(390);
  await page.screenshot({ path: testInfo.outputPath('provider-settings-mobile.png'), fullPage: true, animations: 'disabled' });
  await reset.click();
  const confirmation = page.getByRole('dialog', { name: 'Revert to deployment defaults', exact: true });
  const cleared = page.waitForResponse((response) => response.request().method() === 'DELETE' && new URL(response.url()).pathname === path);
  await confirmation.getByRole('button', { name: 'Revert', exact: true }).click();
  expect((await cleared).status()).toBe(200);
  await expect(settings.getByLabel('Chat Model', { exact: true })).toHaveValue(before.chat_model);
  await expect(settings.getByLabel('Base URL', { exact: true })).toHaveValue(before.base_url);
  await settings.getByRole('button', { name: 'Close', exact: true }).click();
  await page.setViewportSize({ width: 1280, height: 720 });
}
