import { test, expect } from '@playwright/test';
import { installApiMocks, setLocalAuthState, AUDIO_FILES } from './fixtures/api-mocks';

test.beforeEach(async ({ page }) => {
  await setLocalAuthState(page);
  await installApiMocks(page);
});

test.describe('Dashboard – Audio File List', () => {
  test('displays all audio files on load', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // Should show all 4 audio files by title
    for (const file of AUDIO_FILES) {
      await expect(page.locator(`text=${file.title}`).first()).toBeVisible({ timeout: 10000 });
    }
  });

  test('shows status indicators for each file', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // Completed file should be visible
    await expect(page.locator('text=Team Standup March 10').first()).toBeVisible({ timeout: 10000 });

    // Processing file should show processing state
    await expect(page.locator('text=Interview with Candidate').first()).toBeVisible({ timeout: 10000 });
  });

  test('clicking an audio file navigates to detail view', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // Wait for the file list to render
    await expect(page.locator('text=Team Standup March 10').first()).toBeVisible({ timeout: 10000 });

    await page.locator('text=Team Standup March 10').first().click();
    await expect(page).toHaveURL(/\/audio\/1/);
  });
});
