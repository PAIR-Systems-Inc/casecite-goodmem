import { expect, type APIRequestContext, type Page, type TestInfo } from '@playwright/test';

export async function exerciseAdministration(page: Page, request: APIRequestContext, headers: Record<string, string>, testInfo: TestInfo) {
  const health = await request.get('/health', { headers });
  expect(health.status()).toBe(200);
  expect(await health.json()).toMatchObject({ status: 'healthy', components: { vector_db: { type: 'goodmem', healthy: true }, database: { type: 'postgresql', healthy: true } } });
  expect((await request.get('/ready')).status()).toBe(200);
  const before = (await (await request.get('/api/v1/documents', { headers })).json()).documents as Array<{ id: string; memory_id: string; index_memory_id?: string; status: string }>;
  expect(before.length).toBeGreaterThan(1);
  await page.locator('header[data-layout="header"]').getByRole('button', { name: /Settings/ }).click();
  const settings = page.getByRole('dialog', { name: 'Settings', exact: true });
  await settings.getByRole('button', { name: 'Advanced', exact: true }).click();
  const started = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/settings/reindex');
  await settings.getByRole('button', { name: 'Rebuild', exact: true }).click();
  const response = await started;
  expect(response.status()).toBe(200);
  const { job_id } = await response.json();
  await expect.poll(async () => (await (await request.get(`/api/v1/jobs/${job_id}`, { headers })).json()).status, { timeout: 90_000 }).toBe('completed');
  const result = (await (await request.get(`/api/v1/jobs/${job_id}`, { headers })).json()).result;
  expect(result.errors).toEqual([]);
  expect(result.reindexed).toBe(before.length);
  await expect(settings.getByRole('button', { name: 'Rebuild', exact: true })).toBeEnabled();
  const after = (await (await request.get('/api/v1/documents', { headers })).json()).documents as typeof before;
  for (const previous of before) {
    const current = after.find(d => d.id === previous.id)!;
    expect(current.status).toBe('indexed');
    expect(current.memory_id).toBe(previous.memory_id);
    expect(current.index_memory_id).toBeTruthy();
    expect(current.index_memory_id).not.toBe(previous.index_memory_id || previous.memory_id);
  }
  const rebuild = settings.getByRole('button', { name: 'Rebuild', exact: true });
  await rebuild.scrollIntoViewIfNeeded();
  await page.screenshot({ path: testInfo.outputPath('administration-desktop.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 390, height: 844 });
  const toggle = settings.getByRole('switch', { name: 'Workflow Emails', exact: true });
  const knob = toggle.locator('div');
  await toggle.scrollIntoViewIfNeeded();
  const trackBounds = await toggle.boundingBox();
  const knobBounds = await knob.boundingBox();
  expect(trackBounds!.width).toBeGreaterThanOrEqual(44);
  expect(trackBounds!.height).toBeGreaterThanOrEqual(44);
  expect(knobBounds!.x).toBeGreaterThanOrEqual(trackBounds!.x);
  expect(knobBounds!.x + knobBounds!.width).toBeLessThanOrEqual(trackBounds!.x + trackBounds!.width);
  await rebuild.scrollIntoViewIfNeeded();
  const bounds = await rebuild.boundingBox();
  expect(bounds!.x).toBeGreaterThanOrEqual(0);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(390);
  await page.screenshot({ path: testInfo.outputPath('administration-mobile.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 1280, height: 720 });
  await settings.getByRole('button', { name: 'Close', exact: true }).first().click();
  const metrics = await (await request.get('/metrics', { headers })).json();
  expect(metrics.documents.total_count).toBe(before.length);
  expect(metrics.documents.indexed_count).toBe(before.length);
  const prometheus = await request.get('/metrics', { headers: { ...headers, Accept: 'text/plain' } });
  expect(await prometheus.text()).toContain(`casecite_documents_total ${before.length}`);
}
