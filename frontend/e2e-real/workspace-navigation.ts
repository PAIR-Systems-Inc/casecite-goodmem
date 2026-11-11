import { expect, type Page } from '@playwright/test';

async function waitForBootstrap(page: Page, navigate: () => Promise<unknown>) {
  // Health is requested after CSRF initialization and the session probe. Wait
  // for these before logging in or uploading, including after each navigation.
  const bootstrapped = page.waitForResponse(r => new URL(r.url()).pathname === '/health');
  await navigate();
  expect((await bootstrapped).status()).toBe(200);
}

export function openWorkspace(page: Page, path: string) {
  return waitForBootstrap(page, () => page.goto(path));
}

export function reloadWorkspace(page: Page) {
  return waitForBootstrap(page, () => page.reload());
}
