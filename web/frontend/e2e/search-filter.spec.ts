import { test, expect } from '@playwright/test';
import { installApiMocks, setLocalAuthState } from './fixtures/api-mocks';

test.beforeEach(async ({ page }) => {
  await setLocalAuthState(page);
  await installApiMocks(page);
});

test.describe('Search & Filter Audio Files', () => {
  test('search filters results by title', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // Wait for initial list to render
    await expect(page.locator('text=Team Standup March 10').first()).toBeVisible({ timeout: 10000 });

    // Find the search input
    const searchInput = page.locator('input[placeholder*="earch"]').first();
    await expect(searchInput).toBeVisible();

    // Type a search query that matches one file
    await searchInput.fill('Client');
    await page.waitForTimeout(600); // debounce
    await page.waitForLoadState('networkidle');

    // Should show the matching file
    await expect(page.locator('text=Client Call Q1 Planning').first()).toBeVisible({ timeout: 10000 });
  });

  test('search with no results shows empty state', async ({ page }) => {
    // Override the mock to return empty for a specific query
    await page.route('**/api/v1/transcription/list*', (route) => {
      const url = new URL(route.request().url());
      const q = url.searchParams.get('q')?.toLowerCase() ?? '';
      if (q.includes('nonexistent')) {
        return route.fulfill({
          json: { jobs: [], pagination: { page: 1, limit: 20, total: 0, pages: 0 } },
        });
      }
      return route.fallback();
    });

    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // Wait for initial list to render
    await expect(page.locator('text=Team Standup March 10').first()).toBeVisible({ timeout: 10000 });

    const searchInput = page.locator('input[placeholder*="earch"]').first();
    await searchInput.fill('nonexistent');
    await page.waitForTimeout(600);
    await page.waitForLoadState('networkidle');

    // Should show "No recordings found" empty state
    await expect(page.locator('text=No recordings found').first()).toBeVisible({ timeout: 10000 });
  });

  test('clearing search shows all files again', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // Wait for list to render
    await expect(page.locator('text=Team Standup March 10').first()).toBeVisible({ timeout: 10000 });

    const searchInput = page.locator('input[placeholder*="earch"]').first();

    // Search for something
    await searchInput.fill('Product');
    await page.waitForTimeout(600);
    await page.waitForLoadState('networkidle');

    // Clear search
    await searchInput.clear();
    await page.waitForTimeout(600);
    await page.waitForLoadState('networkidle');

    // All files should be visible again
    await expect(page.locator('text=Team Standup March 10').first()).toBeVisible({ timeout: 10000 });
    await expect(page.locator('text=Product Review').first()).toBeVisible({ timeout: 10000 });
  });
});
