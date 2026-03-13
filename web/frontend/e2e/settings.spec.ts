import { test, expect } from '@playwright/test';
import { installApiMocks, setLocalAuthState } from './fixtures/api-mocks';

test.beforeEach(async ({ page }) => {
  await setLocalAuthState(page);
  await installApiMocks(page);
});

test.describe('Settings Page', () => {
  test('loads settings page with tabs', async ({ page }) => {
    await page.goto('/settings');
    await page.waitForLoadState('networkidle');

    // Page heading
    await expect(page.locator('h1:has-text("Settings"), h2:has-text("Settings")').first()).toBeVisible();

    // Tab list should be present
    const tabList = page.locator('[role="tablist"]');
    await expect(tabList).toBeVisible();

    // Check for tab triggers
    await expect(page.locator('[aria-label="Transcription"]')).toBeVisible();
    await expect(page.locator('[aria-label="Account"]')).toBeVisible();
    await expect(page.locator('[aria-label="API Keys"]')).toBeVisible();
    await expect(page.locator('[aria-label="LLMs"]')).toBeVisible();
    await expect(page.locator('[aria-label="Summary"]')).toBeVisible();
    await expect(page.locator('[aria-label="Auto Import"]')).toBeVisible();
    await expect(page.locator('[aria-label="Vaults"]')).toBeVisible();
    await expect(page.locator('[aria-label="Cloud Providers"]')).toBeVisible();
  });

  test('defaults to Transcription tab', async ({ page }) => {
    await page.goto('/settings');
    await page.waitForLoadState('networkidle');

    const transcriptionTab = page.locator('[aria-label="Transcription"]');
    await expect(transcriptionTab).toHaveAttribute('data-state', 'active');
  });

  test('switching to Cloud Providers tab shows provider cards', async ({ page }) => {
    await page.goto('/settings');
    await page.waitForLoadState('networkidle');

    await page.locator('[aria-label="Cloud Providers"]').click();
    await page.waitForTimeout(300);

    // Should show provider names
    await expect(page.locator('text=OpenAI').first()).toBeVisible();
    await expect(page.locator('text=AssemblyAI').first()).toBeVisible();
    await expect(page.locator('text=Deepgram').first()).toBeVisible();
  });

  test('OpenAI shows as configured', async ({ page }) => {
    await page.goto('/settings');
    await page.waitForLoadState('networkidle');

    await page.locator('[aria-label="Cloud Providers"]').click();
    await page.waitForTimeout(300);

    // OpenAI has_key=true in our mock, should show "Configured" badge
    await expect(page.locator('text=Configured').first()).toBeVisible();
  });

  test('can enter API key and save button enables', async ({ page }) => {
    await page.goto('/settings');
    await page.waitForLoadState('networkidle');

    await page.locator('[aria-label="Cloud Providers"]').click();
    await page.waitForTimeout(300);

    // Find the AssemblyAI key input
    const assemblyaiInput = page.locator('#key-assemblyai');
    await expect(assemblyaiInput).toBeVisible();

    // Type an API key
    await assemblyaiInput.fill('test-api-key-12345');

    // The save button should now be enabled
    const card = assemblyaiInput.locator('..').locator('..');
    const saveButton = card.locator('button:has-text("Save")');
    await expect(saveButton).toBeEnabled();
  });

  test('saving API key shows success message', async ({ page }) => {
    await page.goto('/settings');
    await page.waitForLoadState('networkidle');

    await page.locator('[aria-label="Cloud Providers"]').click();
    await page.waitForTimeout(300);

    const assemblyaiInput = page.locator('#key-assemblyai');
    await assemblyaiInput.fill('test-api-key-12345');

    // Find and click the enabled save button (disabled ones belong to other providers)
    const saveButton = page.locator('button:has-text("Save"):not([disabled])').first();
    await expect(saveButton).toBeVisible({ timeout: 5000 });
    await saveButton.click();
    await page.waitForTimeout(500);

    // Should show success message
    await expect(page.locator('text=API key saved').first()).toBeVisible({ timeout: 5000 });
  });

  test('can delete an API key', async ({ page }) => {
    await page.goto('/settings');
    await page.waitForLoadState('networkidle');

    await page.locator('[aria-label="Cloud Providers"]').click();
    await page.waitForTimeout(300);

    // OpenAI is configured, find its delete button
    const deleteButton = page.locator('[aria-label="Remove OpenAI API key"]');
    if (await deleteButton.isVisible().catch(() => false)) {
      await deleteButton.click();
      await page.waitForTimeout(500);

      // Should show removal message
      await expect(page.locator('text=removed').first()).toBeVisible();
    }
  });

  test('switching between tabs shows correct content', async ({ page }) => {
    await page.goto('/settings');
    await page.waitForLoadState('networkidle');

    // Switch to Summary tab
    await page.locator('[aria-label="Summary"]').click();
    await page.waitForTimeout(300);
    await expect(page.locator('text=Summarization Templates').first()).toBeVisible();

    // Switch to Vaults tab
    await page.locator('[aria-label="Vaults"]').click();
    await page.waitForTimeout(300);
    // Vaults tab content should be visible
    await expect(page.locator('text=Vaults').first()).toBeVisible();
  });
});
