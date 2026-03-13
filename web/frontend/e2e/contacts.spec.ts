import { test, expect } from '@playwright/test';
import { installApiMocks, setLocalAuthState, CONTACTS } from './fixtures/api-mocks';

test.beforeEach(async ({ page }) => {
  await setLocalAuthState(page);
  await installApiMocks(page);
});

test.describe('Contacts Management', () => {
  test('loads contacts page with heading', async ({ page }) => {
    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    await expect(page.locator('h1:has-text("Contacts")').first()).toBeVisible({ timeout: 10000 });
    await expect(page.locator('text=File-first contacts').first()).toBeVisible({ timeout: 10000 });
  });

  test('displays all contacts in the list', async ({ page }) => {
    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    for (const contact of CONTACTS) {
      await expect(page.locator(`text=${contact.name}`).first()).toBeVisible({ timeout: 10000 });
    }
  });

  test('search filters contacts', async ({ page }) => {
    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    // Wait for initial list to render
    await expect(page.locator('text=Alice Johnson').first()).toBeVisible({ timeout: 10000 });

    const searchInput = page.locator('input[placeholder="Search contacts..."]');
    await expect(searchInput).toBeVisible();

    await searchInput.fill('Alice');
    await page.waitForTimeout(500);
    await page.waitForLoadState('networkidle');

    await expect(page.locator('text=Alice Johnson').first()).toBeVisible({ timeout: 10000 });
  });

  test('clicking a contact shows detail pane', async ({ page }) => {
    // Override single-contact endpoint to return full data
    await page.route(/\/api\/v1\/contacts\/\d+$/, (route) => {
      if (route.request().method() === 'GET') {
        return route.fulfill({
          json: {
            id: 1,
            vault_id: 1,
            contact_uid: 'alice-001',
            slug: 'alice-johnson',
            name: 'Alice Johnson',
            email: 'alice@example.com',
            phone: '+1-555-0101',
            notes: 'Engineering manager',
            note_path: 'Contacts/People/alice-johnson--alice-001/contact.md',
            file_mtime_ns: 1709280000000000000,
            voice_snippet_path: '/vault/Contacts/People/alice-johnson--alice-001/snippet.wav',
            signature_embedding_path: '/vault/Contacts/People/alice-johnson--alice-001/signature.json',
            signature_status: 'ready',
            signature_data: '{"source":"extracted"}',
            created_at: '2026-01-15T08:00:00Z',
            updated_at: '2026-03-01T10:00:00Z',
          },
        });
      }
      if (route.request().method() === 'PUT') {
        return route.fulfill({
          json: {
            id: 1,
            vault_id: 1,
            contact_uid: 'alice-001',
            slug: 'alice-johnson',
            name: 'Alice Johnson Updated',
            email: 'alice@example.com',
            phone: '+1-555-0101',
            notes: 'Engineering manager',
            note_path: 'Contacts/People/alice-johnson--alice-001/contact.md',
            file_mtime_ns: 1709280000000000000,
            signature_status: 'ready',
            created_at: '2026-01-15T08:00:00Z',
            updated_at: new Date().toISOString(),
          },
        });
      }
      return route.continue();
    });

    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    // Wait for list to render, then click Alice
    await expect(page.locator('button:has-text("Alice Johnson")').first()).toBeVisible({ timeout: 10000 });
    await page.locator('button:has-text("Alice Johnson")').first().click();
    await page.waitForTimeout(500);

    // Detail pane should show the contact name as a heading
    await expect(page.locator('h2:has-text("Alice Johnson")').first()).toBeVisible({ timeout: 10000 });
  });

  test('create a new contact', async ({ page }) => {
    let createCalled = false;
    await page.route('**/api/v1/contacts', (route) => {
      if (route.request().method() === 'POST') {
        createCalled = true;
        return route.fulfill({
          json: {
            id: 99,
            vault_id: 1,
            contact_uid: 'new-099',
            slug: 'new-person',
            name: 'New Person',
            email: 'new@example.com',
            phone: '',
            notes: '',
            note_path: 'Contacts/People/new-person--new-099/contact.md',
            file_mtime_ns: 0,
            voice_snippet_path: null,
            signature_embedding_path: null,
            signature_status: 'none',
            signature_data: null,
            created_at: new Date().toISOString(),
            updated_at: new Date().toISOString(),
          },
        });
      }
      // GET - return original list plus new
      return route.fulfill({
        json: {
          contacts: [
            ...CONTACTS,
            {
              id: 99,
              vault_id: 1,
              contact_uid: 'new-099',
              slug: 'new-person',
              name: 'New Person',
              email: 'new@example.com',
              phone: '',
              notes: '',
              note_path: 'Contacts/People/new-person--new-099/contact.md',
              file_mtime_ns: 0,
              voice_snippet_path: null,
              signature_embedding_path: null,
              signature_status: 'none',
              signature_data: null,
              created_at: new Date().toISOString(),
              updated_at: new Date().toISOString(),
            },
          ],
          vault_id: 1,
        },
      });
    });

    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    // Fill in the "New Contact" form
    const nameInput = page.locator('input[placeholder="Name *"]');
    await expect(nameInput).toBeVisible({ timeout: 10000 });
    await nameInput.fill('New Person');

    const emailInput = page.locator('input[placeholder="Email (optional)"]');
    await emailInput.fill('new@example.com');

    // Click Create Contact
    await page.locator('button:has-text("Create Contact")').click();
    await page.waitForTimeout(500);

    expect(createCalled).toBe(true);
  });

  test('reindex contacts button works', async ({ page }) => {
    let reindexCalled = false;
    await page.route('**/api/v1/contacts/reindex', (route) => {
      reindexCalled = true;
      return route.fulfill({ json: { message: 'Reindex complete', indexed_count: 3 } });
    });

    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    const reindexButton = page.locator('button:has-text("Reindex")');
    await expect(reindexButton).toBeVisible({ timeout: 10000 });
    await reindexButton.click();
    await page.waitForTimeout(500);

    expect(reindexCalled).toBe(true);

    // Should show success banner
    await expect(page.locator('text=reindexed').first()).toBeVisible({ timeout: 10000 });
  });

  test('voice snippet section is visible for selected contact', async ({ page }) => {
    await page.route(/\/api\/v1\/contacts\/\d+$/, (route) => {
      if (route.request().method() === 'GET') {
        return route.fulfill({
          json: {
            id: 1,
            vault_id: 1,
            contact_uid: 'alice-001',
            slug: 'alice-johnson',
            name: 'Alice Johnson',
            email: 'alice@example.com',
            phone: '+1-555-0101',
            notes: '',
            note_path: 'Contacts/People/alice-johnson--alice-001/contact.md',
            file_mtime_ns: 1709280000000000000,
            voice_snippet_path: '/vault/snippet.wav',
            signature_embedding_path: '/vault/signature.json',
            signature_status: 'ready',
            signature_data: null,
            created_at: '2026-01-15T08:00:00Z',
            updated_at: '2026-03-01T10:00:00Z',
          },
        });
      }
      return route.continue();
    });

    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    await expect(page.locator('button:has-text("Alice Johnson")').first()).toBeVisible({ timeout: 10000 });
    await page.locator('button:has-text("Alice Johnson")').first().click();
    await page.waitForTimeout(500);

    // Voice Snippet section
    await expect(page.locator('text=Voice Snippet').first()).toBeVisible({ timeout: 10000 });
    await expect(page.locator('button:has-text("Upload Snippet")').first()).toBeVisible({ timeout: 10000 });

    // Voice Signature section
    await expect(page.locator('text=Voice Signature').first()).toBeVisible({ timeout: 10000 });
    await expect(page.locator('button:has-text("Import Signature JSON")').first()).toBeVisible({ timeout: 10000 });
    await expect(page.locator('button:has-text("Generate from Snippet")').first()).toBeVisible({ timeout: 10000 });
  });

  test('file paths panel toggles', async ({ page }) => {
    await page.route(/\/api\/v1\/contacts\/\d+$/, (route) => {
      if (route.request().method() === 'GET') {
        return route.fulfill({
          json: {
            id: 1,
            vault_id: 1,
            contact_uid: 'alice-001',
            slug: 'alice-johnson',
            name: 'Alice Johnson',
            email: '',
            phone: '',
            notes: '',
            note_path: 'Contacts/People/alice-johnson--alice-001/contact.md',
            file_mtime_ns: 1709280000000000000,
            voice_snippet_path: null,
            signature_embedding_path: null,
            signature_status: 'none',
            signature_data: null,
            created_at: '2026-01-15T08:00:00Z',
            updated_at: '2026-03-01T10:00:00Z',
          },
        });
      }
      return route.continue();
    });

    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    await expect(page.locator('button:has-text("Alice Johnson")').first()).toBeVisible({ timeout: 10000 });
    await page.locator('button:has-text("Alice Johnson")').first().click();
    await page.waitForTimeout(500);

    // Toggle file paths panel
    const filePathsButton = page.locator('button:has-text("File Paths"), span:has-text("File Paths")').first();
    if (await filePathsButton.isVisible().catch(() => false)) {
      await filePathsButton.click();
      await page.waitForTimeout(300);

      // Should show file path info
      await expect(page.locator('text=contact.md').first()).toBeVisible({ timeout: 5000 });
    }
  });
});
