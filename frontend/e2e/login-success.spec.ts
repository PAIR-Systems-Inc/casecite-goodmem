import { test, expect } from '@playwright/test';
import {
  installApiMocks, submitLogin, expectSignedIn, loginModal, fulfillJson, TEST_USER,
} from './helpers/auth';

// Sign-in from the header with the backend mocked (helpers/auth.ts): the
// login POST, the session probe, settings, documents, connectors and the rest
// of the per-sign-in loads are all answered locally.
test.describe('Login', () => {
  // At 720px tall the sidebar's sticky upload panel overlaps the last nav
  // item (Knowledge Base); a laptop-height viewport keeps every item clickable.
  test.use({ viewport: { width: 1280, height: 1000 } });

  test('signing in closes the modal, shows the user and unlocks navigation', async ({ page }) => {
    const state = await installApiMocks(page);
    await page.goto('/');

    // Logged-out shell: auth overlay in the main area, locked sidebar.
    await expect(page.getByRole('heading', { name: 'Welcome to CaseCite' })).toBeVisible();
    const sidebar = page.locator('aside[data-layout="left-sidebar"]');
    await expect(sidebar.locator('button[class*="modeBtn"]').first()).toHaveClass(/Locked/);

    const loginRequest = page.waitForRequest(
      (req) => req.method() === 'POST' && /\/api\/v1\/auth\/login$/.test(req.url()),
    );
    await submitLogin(page);

    // The form sends exactly what the API expects.
    const body = (await loginRequest).postDataJSON();
    expect(body).toEqual({ email: TEST_USER.email, password: 'correct-horse-battery' });

    // Modal gone, header shows the user, overlay replaced by the research view.
    await expect(loginModal(page)).toHaveCount(0);
    await expectSignedIn(page);
    await expect(page.getByRole('heading', { name: 'Welcome to CaseCite' })).toHaveCount(0);
    await expect(page.getByText('Signed in successfully')).toBeVisible();

    // Settings is only offered to signed-in users.
    const header = page.locator('header[data-layout="header"]');
    await expect(header.getByRole('button', { name: /Settings/ })).toBeVisible();

    // Navigation is unlocked: no lock class, and clicking navigates instead of
    // prompting registration.
    await expect(sidebar.locator('button[class*="modeBtn"]').first()).not.toHaveClass(/Locked/);
    await sidebar.getByRole('button', { name: /Knowledge Base/ }).click();
    await expect(page).toHaveURL(/\/documents$/);
    await expect(page.getByRole('heading', { name: 'Knowledge Base' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Register' })).toHaveCount(0);

    // The authenticated state was reached through the real post-login loads.
    expect(state.calls).toContain('GET /settings/rag');
    expect(state.calls).toContain('GET /connectors');
    expect(state.calls.some((c) => c === 'GET /documents')).toBe(true);
  });

  test('wrong credentials keep the modal open and show the server message', async ({ page }) => {
    await installApiMocks(page, {
      overrides: [
        async (route, url, method) => {
          if (method !== 'POST' || !url.pathname.endsWith('/auth/login')) return false;
          await fulfillJson(route, 401, { detail: 'Invalid email or password' });
          return true;
        },
      ],
    });
    await page.goto('/');
    await submitLogin(page, TEST_USER.email, 'wrong-password');

    const modal = loginModal(page);
    const errorBox = modal.locator('[class*="errorBox"]');
    await expect(errorBox).toBeVisible({ timeout: 5000 });
    await expect(errorBox).toHaveText('Invalid email or password');

    // Still logged out: modal open, submit re-enabled, header unchanged.
    await expect(modal.getByRole('heading', { name: 'Sign In' })).toBeVisible();
    await expect(modal.getByRole('button', { name: 'Sign In', exact: true })).toBeEnabled();
    const header = page.locator('header[data-layout="header"]');
    await expect(header.getByRole('button', { name: /Register/i })).toBeVisible();
    await expect(header.getByRole('button', { name: 'Log out' })).toHaveCount(0);
  });
});
