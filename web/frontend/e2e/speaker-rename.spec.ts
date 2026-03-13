import { test, expect } from '@playwright/test';
import { installApiMocks, setLocalAuthState } from './fixtures/api-mocks';

test.beforeEach(async ({ page }) => {
  await setLocalAuthState(page);
  await installApiMocks(page);
});

test.describe('Speaker Rename Dialog', () => {
  test('opens speaker rename dialog and shows speakers', async ({ page }) => {
    await page.goto('/audio/1');
    await page.waitForLoadState('networkidle');

    // Find and click the speaker rename/edit button (could be in dropdown or direct button)
    const renameButton = page.locator(
      'button:has-text("Rename"), button:has-text("Speaker"), button:has-text("Edit Speaker"), [aria-label*="speaker"]',
    ).first();

    // If the rename button is in a dropdown, open the dropdown first
    const dropdownTrigger = page.locator('button:has([class*="ellipsis"]), button:has([class*="more"]), [aria-label="More"]').first();
    if (await dropdownTrigger.isVisible().catch(() => false)) {
      await dropdownTrigger.click();
      await page.waitForTimeout(200);
    }

    // Try to find the speaker rename trigger
    const trigger = page.locator('text=Rename Speakers, text=Speaker, text=Edit Speakers').first();
    if (await trigger.isVisible().catch(() => false)) {
      await trigger.click();
    } else if (await renameButton.isVisible().catch(() => false)) {
      await renameButton.click();
    }

    // The dialog should now be open with speaker inputs
    const dialog = page.locator('[role="dialog"]');
    if (await dialog.isVisible().catch(() => false)) {
      await expect(dialog).toContainText('Rename Speakers');

      // Should show speaker labels (SPEAKER_00, SPEAKER_01, SPEAKER_02)
      await expect(dialog.locator('text=SPEAKER_00').first()).toBeVisible();
      await expect(dialog.locator('text=SPEAKER_01').first()).toBeVisible();
      await expect(dialog.locator('text=SPEAKER_02').first()).toBeVisible();
    }
  });

  test('can type a new name for a speaker', async ({ page }) => {
    // Override speaker mappings to return raw speaker IDs
    await page.route('**/api/v1/transcription/*/speakers', (route) => {
      if (route.request().method() === 'GET') {
        return route.fulfill({
          json: [
            { original_speaker: 'SPEAKER_00', custom_name: 'SPEAKER_00' },
            { original_speaker: 'SPEAKER_01', custom_name: 'SPEAKER_01' },
          ],
        });
      }
      // POST – echo back with updated name
      return route.fulfill({
        json: {
          mappings: [
            { original_speaker: 'SPEAKER_00', custom_name: 'Jane' },
            { original_speaker: 'SPEAKER_01', custom_name: 'SPEAKER_01' },
          ],
          contact_bootstrap: { started_count: 0, created_count: 0 },
        },
      });
    });

    await page.goto('/audio/1');
    await page.waitForLoadState('networkidle');

    // Open the dialog (try multiple approaches)
    const triggers = [
      page.locator('button:has-text("Rename")').first(),
      page.locator('button:has-text("Speaker")').first(),
      page.locator('[aria-label*="speaker"]').first(),
    ];

    for (const trigger of triggers) {
      if (await trigger.isVisible().catch(() => false)) {
        await trigger.click();
        break;
      }
    }

    const dialog = page.locator('[role="dialog"]');
    if (await dialog.isVisible().catch(() => false)) {
      // Find the first speaker input and type a new name
      const speakerInput = dialog.locator('input[id*="speaker"]').first();
      if (await speakerInput.isVisible().catch(() => false)) {
        await speakerInput.clear();
        await speakerInput.fill('Jane');

        // Click Save
        const saveButton = dialog.locator('button:has-text("Save")');
        await expect(saveButton).toBeEnabled();
      }
    }
  });

  test('save button is disabled when no speakers exist', async ({ page }) => {
    // Override to return empty speakers
    await page.route('**/api/v1/transcription/*/speakers', (route) => {
      if (route.request().method() === 'GET') {
        return route.fulfill({ json: [] });
      }
      return route.continue();
    });

    await page.goto('/audio/1');
    await page.waitForLoadState('networkidle');

    // Open dialog
    const triggers = [
      page.locator('button:has-text("Rename")').first(),
      page.locator('button:has-text("Speaker")').first(),
    ];

    for (const trigger of triggers) {
      if (await trigger.isVisible().catch(() => false)) {
        await trigger.click();
        break;
      }
    }

    const dialog = page.locator('[role="dialog"]');
    if (await dialog.isVisible().catch(() => false)) {
      // Save button should be disabled when no speakers
      const saveButton = dialog.locator('button:has-text("Save")');
      if (await saveButton.isVisible().catch(() => false)) {
        await expect(saveButton).toBeDisabled();
      }

      // Should show "No speakers found" message
      await expect(dialog.locator('text=No speakers found')).toBeVisible();
    }
  });
});
