import { test, expect } from '@playwright/test';
import { installApiMocks, setLocalAuthState } from './fixtures/api-mocks';

test.beforeEach(async ({ page }) => {
  await setLocalAuthState(page);
  await installApiMocks(page);
});

test.describe('Batch Selection', () => {
  test('shift-click selects a file and shows the bulk action bar', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // Wait for files to render
    await expect(page.locator('text=Team Standup March 10').first()).toBeVisible({ timeout: 10000 });

    // Shift-click the first file to enter selection mode
    await page.locator('text=Team Standup March 10').first().click({ modifiers: ['Shift'] });

    // The bulk action bar should appear with "1" badge and "Selected" label
    await expect(page.locator('text=Selected').first()).toBeVisible({ timeout: 5000 });
  });

  test('shift-click multiple files shows correct count', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await expect(page.locator('text=Team Standup March 10').first()).toBeVisible({ timeout: 10000 });

    // Select two files
    await page.locator('text=Team Standup March 10').first().click({ modifiers: ['Shift'] });
    await page.locator('text=Product Review').first().click();

    // Badge should show 2
    await expect(page.locator('text=Selected').first()).toBeVisible({ timeout: 5000 });
  });

  test('clear selection button hides the bulk action bar', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await expect(page.locator('text=Team Standup March 10').first()).toBeVisible({ timeout: 10000 });

    // Enter selection mode
    await page.locator('text=Team Standup March 10').first().click({ modifiers: ['Shift'] });
    await expect(page.locator('text=Selected').first()).toBeVisible({ timeout: 5000 });

    // Click the X (clear selection) button — it's the last icon button in the bar
    const clearButton = page.locator('button:has(svg.lucide-x)').last();
    await clearButton.click();

    // Bulk bar should disappear
    await expect(page.locator('text=Selected')).not.toBeVisible({ timeout: 5000 });
  });
});

test.describe('Batch Delete', () => {
  test('delete selected files shows confirmation and completes', async ({ page }) => {
    let batchDeleteCalled = false;
    await page.route('**/api/v1/transcription/batch/delete', (route) => {
      batchDeleteCalled = true;
      const body = route.request().postDataJSON();
      const results = (body.ids || []).map((id: string) => ({ id, success: true }));
      return route.fulfill({ json: { results } });
    });

    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await expect(page.locator('text=Team Standup March 10').first()).toBeVisible({ timeout: 10000 });

    // Select a file
    await page.locator('text=Team Standup March 10').first().click({ modifiers: ['Shift'] });
    await expect(page.locator('text=Selected').first()).toBeVisible({ timeout: 5000 });

    // Click the delete button in the bulk bar (Trash2 icon)
    const deleteButton = page.locator('button:has(svg.lucide-trash-2)').last();
    await deleteButton.click();

    // Confirmation dialog should appear
    await expect(page.locator('text=Are you sure?').first()).toBeVisible({ timeout: 5000 });
    await expect(page.locator('text=permanently delete').first()).toBeVisible();

    // Confirm deletion
    const confirmButton = page.locator('button:has-text("Delete")').last();
    await confirmButton.click();

    // Dialog should close and batch delete should have been called
    await expect(page.locator('text=Are you sure?')).not.toBeVisible({ timeout: 5000 });
    expect(batchDeleteCalled).toBe(true);
  });
});

test.describe('Batch Move to Folder', () => {
  test('move selected files to a folder via bulk action bar', async ({ page }) => {
    let batchMoveCalled = false;
    let moveFolder = '';
    await page.route('**/api/v1/transcription/batch/move', (route) => {
      batchMoveCalled = true;
      const body = route.request().postDataJSON();
      moveFolder = body.folder;
      const results = (body.ids || []).map((id: string) => ({ id, success: true }));
      return route.fulfill({ json: { results } });
    });

    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await expect(page.locator('text=Team Standup March 10').first()).toBeVisible({ timeout: 10000 });

    // Select a file
    await page.locator('text=Team Standup March 10').first().click({ modifiers: ['Shift'] });
    await expect(page.locator('text=Selected').first()).toBeVisible({ timeout: 5000 });

    // Click the move-to-folder button (FolderInput icon)
    const moveButton = page.locator('button:has(svg.lucide-folder-input)').last();
    await moveButton.click();

    // Dropdown should show folder options
    await expect(page.locator('[role="menuitem"]:has-text("Clients")').first()).toBeVisible({ timeout: 5000 });

    // Click Clients
    await page.locator('[role="menuitem"]:has-text("Clients")').first().click();

    // Batch move should have been called with "Clients" folder
    expect(batchMoveCalled).toBe(true);
    expect(moveFolder).toBe('Clients');
  });

  test('move selected files to Unfiled (root)', async ({ page }) => {
    let moveFolder: string | null = null;
    await page.route('**/api/v1/transcription/batch/move', (route) => {
      const body = route.request().postDataJSON();
      moveFolder = body.folder;
      const results = (body.ids || []).map((id: string) => ({ id, success: true }));
      return route.fulfill({ json: { results } });
    });

    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await expect(page.locator('text=Client Call Q1 Planning').first()).toBeVisible({ timeout: 10000 });

    // Select a file
    await page.locator('text=Client Call Q1 Planning').first().click({ modifiers: ['Shift'] });
    await expect(page.locator('text=Selected').first()).toBeVisible({ timeout: 5000 });

    // Open move dropdown
    const moveButton = page.locator('button:has(svg.lucide-folder-input)').last();
    await moveButton.click();

    // Click Unfiled
    await page.locator('[role="menuitem"]:has-text("Unfiled")').first().click();

    // Should move to root (empty string)
    expect(moveFolder).toBe('');
  });
});
