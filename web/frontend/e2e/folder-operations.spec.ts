import { test, expect } from '@playwright/test';
import { installApiMocks, setLocalAuthState, AUDIO_FILES, FOLDERS } from './fixtures/api-mocks';

test.beforeEach(async ({ page }) => {
  await setLocalAuthState(page);
  await installApiMocks(page);
});

test.describe('Folder Sidebar', () => {
  test('renders All Files, Unfiled, and folder tree', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // Sidebar should show All Files and Unfiled
    await expect(page.locator('text=All Files').first()).toBeVisible({ timeout: 10000 });
    await expect(page.locator('text=Unfiled').first()).toBeVisible({ timeout: 10000 });

    // Folder tree should render top-level folders
    await expect(page.locator('text=Work').first()).toBeVisible({ timeout: 10000 });
    await expect(page.locator('text=Clients').first()).toBeVisible({ timeout: 10000 });
  });

  test('clicking a folder filters the file list', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // All files should be visible initially
    await expect(page.locator('text=Team Standup March 10').first()).toBeVisible({ timeout: 10000 });
    await expect(page.locator('text=Client Call Q1 Planning').first()).toBeVisible({ timeout: 10000 });

    // Click the Clients folder in sidebar
    const clientsFolder = page.locator('text=Clients').first();
    await clientsFolder.click();
    await page.waitForTimeout(500);
    await page.waitForLoadState('networkidle');

    // Only the Client Call should be visible
    await expect(page.locator('text=Client Call Q1 Planning').first()).toBeVisible({ timeout: 10000 });
    // Other files should not be visible
    await expect(page.locator('text=Team Standup March 10')).toHaveCount(0, { timeout: 5000 });
  });

  test('clicking Unfiled shows only unfiled files', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    await expect(page.locator('text=Team Standup March 10').first()).toBeVisible({ timeout: 10000 });

    // Click Unfiled
    const unfiledItem = page.locator('text=Unfiled').first();
    await unfiledItem.click();
    await page.waitForTimeout(500);
    await page.waitForLoadState('networkidle');

    // Only the unfiled file (Interview with Candidate, folder: '') should show
    await expect(page.locator('text=Interview with Candidate').first()).toBeVisible({ timeout: 10000 });
    await expect(page.locator('text=Team Standup March 10')).toHaveCount(0, { timeout: 5000 });
  });

  test('clicking All Files shows all files again', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // First filter to a folder
    await page.locator('text=Clients').first().click();
    await page.waitForTimeout(500);
    await page.waitForLoadState('networkidle');
    await expect(page.locator('text=Team Standup March 10')).toHaveCount(0, { timeout: 5000 });

    // Click All Files to reset
    await page.locator('text=All Files').first().click();
    await page.waitForTimeout(500);
    await page.waitForLoadState('networkidle');

    // All files visible again
    await expect(page.locator('text=Team Standup March 10').first()).toBeVisible({ timeout: 10000 });
    await expect(page.locator('text=Client Call Q1 Planning').first()).toBeVisible({ timeout: 10000 });
  });
});

test.describe('Folder CRUD', () => {
  test('create a new folder via the + button', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // Wait for folders to load
    await expect(page.locator('text=Work').first()).toBeVisible({ timeout: 10000 });

    // Click the + button in the sidebar header (FolderPlus icon)
    const addButton = page.locator('button:has(svg.lucide-folder-plus)').first();
    await addButton.click();

    // Input should appear
    const input = page.locator('input[placeholder="Folder name..."]');
    await expect(input).toBeVisible({ timeout: 5000 });

    // Type a folder name and submit
    await input.fill('Personal');
    await input.press('Enter');

    // Input should disappear after creation
    await expect(input).not.toBeVisible({ timeout: 5000 });
  });

  test('rename a folder via context menu', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await expect(page.getByText('Clients', { exact: true }).first()).toBeVisible({ timeout: 10000 });

    // Find the Clients text span and its parent group row
    const clientsSpan = page.getByText('Clients', { exact: true }).first();
    const folderRow = clientsSpan.locator('xpath=ancestor::div[contains(@class, "group")]').first();
    await folderRow.hover();

    // Force-click the more button (starts with opacity-0, revealed via group-hover)
    const moreButton = folderRow.locator('button').last();
    await moreButton.click({ force: true });

    // Click Rename in the dropdown
    await page.locator('[role="menuitem"]:has-text("Rename")').click();

    // Rename input should appear (no type attribute, so match by position — it's the last input)
    const renameInput = page.locator('input').last();
    await expect(renameInput).toBeVisible({ timeout: 5000 });

    // Clear and type new name
    await renameInput.clear();
    await renameInput.fill('Partners');
    await renameInput.press('Enter');
  });

  test('delete a folder via context menu shows confirmation', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');
    await expect(page.getByText('Clients', { exact: true }).first()).toBeVisible({ timeout: 10000 });

    // Find the Clients text span and its parent group row
    const clientsSpan = page.getByText('Clients', { exact: true }).first();
    const folderRow = clientsSpan.locator('xpath=ancestor::div[contains(@class, "group")]').first();
    await folderRow.hover();

    // Force-click the more button (starts with opacity-0, revealed via group-hover)
    const moreButton = folderRow.locator('button').last();
    await moreButton.click({ force: true });

    // Click Delete
    await page.locator('[role="menuitem"]:has-text("Delete")').click();

    // Confirmation dialog should appear
    await expect(page.locator('text=Delete Folder').first()).toBeVisible({ timeout: 5000 });
    await expect(page.locator('text=Are you sure you want to delete').first()).toBeVisible();

    // Click the Delete confirmation button
    await page.locator('button:has-text("Delete")').last().click();

    // Dialog should close
    await expect(page.locator('text=Delete Folder')).not.toBeVisible({ timeout: 5000 });
  });
});
