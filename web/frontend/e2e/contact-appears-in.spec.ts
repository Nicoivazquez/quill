/**
 * E2E tests for the Contact "Appears In" navigation flow.
 *
 * Regression coverage for: clicking a transcript in a contact's "Appears In"
 * section must navigate to /audio/{job_id}, NOT /transcription/{job_id}.
 *
 * Related fix: ContactsPage.tsx line ~629
 *   onClick={() => navigate(`/audio/${appearance.job_id}`)}
 */

import { test, expect } from '@playwright/test';
import {
  installApiMocks,
  setLocalAuthState,
  CONTACTS,
  CONTACT_APPEARANCES,
} from './fixtures/api-mocks';

// ── Helpers ──────────────────────────────────────────────────────────────────

/** Open a contact's detail pane by clicking its name in the list. */
async function openContactDetail(page: import('@playwright/test').Page, contactName: string) {
  await expect(page.locator(`button:has-text("${contactName}")`).first()).toBeVisible({
    timeout: 10_000,
  });
  await page.locator(`button:has-text("${contactName}")`).first().click();
  // Wait for the detail pane heading to confirm selection.
  await expect(page.locator(`h2:has-text("${contactName}")`).first()).toBeVisible({
    timeout: 10_000,
  });
}

/** Expand the "Appears In" collapsible section. */
async function expandAppearsIn(page: import('@playwright/test').Page) {
  const toggle = page.locator('button:has-text("Appears In")').first();
  await expect(toggle).toBeVisible({ timeout: 10_000 });

  // Only click if the section is currently collapsed (shows "Show", not "Hide").
  const toggleText = await toggle.textContent();
  if (toggleText?.includes('Show')) {
    await toggle.click();
  }

  // Wait until at least one appearance row is rendered.
  await expect(
    page.locator('button:has-text("Team Standup March 10"), button:has-text("Client Call Q1 Planning")').first(),
  ).toBeVisible({ timeout: 10_000 });
}

// ── Test setup ───────────────────────────────────────────────────────────────

test.beforeEach(async ({ page }) => {
  await setLocalAuthState(page);
  await installApiMocks(page);

  // Override single-contact endpoint to always return Alice (has a ready voice
  // signature so the full detail pane, including "Appears In", is visible).
  await page.route(/\/api\/v1\/contacts\/\d+$/, (route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({
        json: {
          ...CONTACTS[0],
          signature_status: 'ready',
        },
      });
    }
    return route.continue();
  });
});

// ── Test suite ───────────────────────────────────────────────────────────────

test.describe('Contact "Appears In" navigation', () => {
  test('Appears In section is visible and shows appearance count badge', async ({ page }) => {
    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    await openContactDetail(page, 'Alice Johnson');

    // The toggle button should exist and show the count badge.
    const toggle = page.locator('button:has-text("Appears In")').first();
    await expect(toggle).toBeVisible({ timeout: 10_000 });
    await expect(toggle).toContainText(`(${CONTACT_APPEARANCES.length})`);
  });

  test('expanding Appears In section reveals transcript rows', async ({ page }) => {
    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    await openContactDetail(page, 'Alice Johnson');
    await expandAppearsIn(page);

    // Every mocked appearance title should be present.
    for (const appearance of CONTACT_APPEARANCES) {
      await expect(page.locator(`button:has-text("${appearance.title}")`).first()).toBeVisible({
        timeout: 10_000,
      });
    }
  });

  test('appearance row shows speaker label, match source, and confidence', async ({ page }) => {
    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    await openContactDetail(page, 'Alice Johnson');
    await expandAppearsIn(page);

    const firstAppearance = CONTACT_APPEARANCES[0];
    const row = page
      .locator(`button:has-text("${firstAppearance.title}")`)
      .first()
      .locator('..');

    // The sub-line should contain speaker label, match source, and confidence %.
    await expect(row).toContainText(firstAppearance.speaker_label);
    await expect(row).toContainText(firstAppearance.match_source);
    await expect(row).toContainText(`${Math.round(firstAppearance.confidence_score * 100)}%`);
  });

  test('clicking an appearance navigates to /audio/{job_id} (not /transcription/{job_id})', async ({
    page,
  }) => {
    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    await openContactDetail(page, 'Alice Johnson');
    await expandAppearsIn(page);

    const targetAppearance = CONTACT_APPEARANCES[0]; // job_id: '1'

    // Click the appearance row.
    await page.locator(`button:has-text("${targetAppearance.title}")`).first().click();

    // REGRESSION: must route to /audio/<id>, never /transcription/<id>.
    await expect(page).toHaveURL(`/audio/${targetAppearance.job_id}`, { timeout: 10_000 });
  });

  test('navigating to /audio/{job_id} lands on the transcript detail page', async ({ page }) => {
    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    await openContactDetail(page, 'Alice Johnson');
    await expandAppearsIn(page);

    const targetAppearance = CONTACT_APPEARANCES[0];
    await page.locator(`button:has-text("${targetAppearance.title}")`).first().click();

    await expect(page).toHaveURL(`/audio/${targetAppearance.job_id}`, { timeout: 10_000 });

    // The AudioDetailView should render (transcript title visible in page).
    await expect(
      page.locator(`text=${targetAppearance.title}`).first(),
    ).toBeVisible({ timeout: 10_000 });
  });

  test('clicking a second appearance navigates to its own /audio/{job_id}', async ({ page }) => {
    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    await openContactDetail(page, 'Alice Johnson');
    await expandAppearsIn(page);

    const secondAppearance = CONTACT_APPEARANCES[1]; // job_id: '4'
    await page.locator(`button:has-text("${secondAppearance.title}")`).first().click();

    await expect(page).toHaveURL(`/audio/${secondAppearance.job_id}`, { timeout: 10_000 });
  });

  test('navigating away from the detail page and back preserves route correctness', async ({
    page,
  }) => {
    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    await openContactDetail(page, 'Alice Johnson');
    await expandAppearsIn(page);

    const targetAppearance = CONTACT_APPEARANCES[0];
    await page.locator(`button:has-text("${targetAppearance.title}")`).first().click();
    await expect(page).toHaveURL(`/audio/${targetAppearance.job_id}`, { timeout: 10_000 });

    // Go back to contacts.
    await page.goBack();
    await expect(page).toHaveURL('/contacts', { timeout: 10_000 });

    // Navigate again; route should still be /audio/*, not /transcription/*.
    await openContactDetail(page, 'Alice Johnson');
    await expandAppearsIn(page);
    await page.locator(`button:has-text("${targetAppearance.title}")`).first().click();
    await expect(page).toHaveURL(`/audio/${targetAppearance.job_id}`, { timeout: 10_000 });
  });

  test('contact with no appearances shows empty state message', async ({ page }) => {
    // Override appearances endpoint to return empty list for this test.
    await page.route('**/api/v1/contacts/*/appearances', (route) =>
      route.fulfill({ json: { appearances: [] } }),
    );

    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    await openContactDetail(page, 'Alice Johnson');

    // Expand even with empty data (toggle should still exist).
    const toggle = page.locator('button:has-text("Appears In")').first();
    await expect(toggle).toBeVisible({ timeout: 10_000 });
    await toggle.click();
    await page.waitForTimeout(300);

    await expect(
      page.locator('text=No transcript appearances found.').first(),
    ).toBeVisible({ timeout: 10_000 });
  });

  test('Appears In count badge is absent when there are no appearances', async ({ page }) => {
    await page.route('**/api/v1/contacts/*/appearances', (route) =>
      route.fulfill({ json: { appearances: [] } }),
    );

    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    await openContactDetail(page, 'Alice Johnson');

    // The count badge span (shown as "(N)") must not be rendered for 0 items.
    const toggle = page.locator('button:has-text("Appears In")').first();
    await expect(toggle).toBeVisible({ timeout: 10_000 });
    await expect(toggle).not.toContainText('(0)');
  });
});
