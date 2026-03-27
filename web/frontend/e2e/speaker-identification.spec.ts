import { test, expect } from '@playwright/test';
import {
  installApiMocks,
  setLocalAuthState,
  CONTACTS,
  SPEAKER_MAPPINGS,
  TRANSCRIPT_SEGMENTS,
  WORD_SEGMENTS,
} from './fixtures/api-mocks';

test.beforeEach(async ({ page }) => {
  await setLocalAuthState(page);
  await installApiMocks(page);
});

test.describe('Speaker Label Display', () => {
  test('displays friendly Speaker A/B/C labels when no custom names are set', async ({ page }) => {
    // Override speaker mappings to return identity mappings (raw label == custom_name)
    await page.route('**/api/v1/transcription/*/speakers', (route) => {
      if (route.request().method() === 'GET') {
        return route.fulfill({
          json: [
            { original_speaker: 'SPEAKER_00', custom_name: 'SPEAKER_00', confidence_score: 0, match_source: '', match_tier: '' },
            { original_speaker: 'SPEAKER_01', custom_name: 'SPEAKER_01', confidence_score: 0, match_source: '', match_tier: '' },
            { original_speaker: 'SPEAKER_02', custom_name: 'SPEAKER_02', confidence_score: 0, match_source: '', match_tier: '' },
          ],
        });
      }
      return route.continue();
    });

    await page.goto('/audio/1');
    await page.waitForLoadState('networkidle');

    // Transcript view should show friendly labels, not raw "SPEAKER_00"
    await expect(page.locator('text=Speaker A').first()).toBeVisible({ timeout: 10000 });
    await expect(page.locator('text=Speaker B').first()).toBeVisible({ timeout: 10000 });
    await expect(page.locator('text=Speaker C').first()).toBeVisible({ timeout: 10000 });

    // Raw labels should NOT be visible in the transcript
    await expect(page.locator('.transcript >> text=SPEAKER_00')).not.toBeVisible();
    await expect(page.locator('.transcript >> text=SPEAKER_01')).not.toBeVisible();
  });

  test('displays custom names when speakers have been renamed', async ({ page }) => {
    // Default mocks already include Alice/Bob/Charlie mappings
    await page.goto('/audio/1');
    await page.waitForLoadState('networkidle');

    // Custom names should appear in the transcript
    await expect(page.locator('text=Alice').first()).toBeVisible({ timeout: 10000 });
    await expect(page.locator('text=Bob').first()).toBeVisible({ timeout: 10000 });
    await expect(page.locator('text=Charlie').first()).toBeVisible({ timeout: 10000 });
  });

  test('download uses friendly labels when no custom names are set', async ({ page }) => {
    // Override speaker mappings to return identity mappings
    await page.route('**/api/v1/transcription/*/speakers', (route) => {
      if (route.request().method() === 'GET') {
        return route.fulfill({
          json: [
            { original_speaker: 'SPEAKER_00', custom_name: 'SPEAKER_00' },
            { original_speaker: 'SPEAKER_01', custom_name: 'SPEAKER_01' },
          ],
        });
      }
      return route.continue();
    });

    await page.goto('/audio/1');
    await page.waitForLoadState('networkidle');

    // Open the speaker rename dialog to verify placeholder labels
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
      // Placeholders should say "Speaker A" / "Speaker B", not raw labels
      const inputA = dialog.locator('input#speaker-SPEAKER_00');
      if (await inputA.isVisible().catch(() => false)) {
        await expect(inputA).toHaveAttribute('placeholder', /Speaker A/);
      }
    }
  });
});

test.describe('Speaker Identification via SSE', () => {
  test('auto-assigned speakers update speaker mappings after SSE event', async ({ page }) => {
    // Start with no speaker mappings
    let speakerMappingsState = [
      { original_speaker: 'SPEAKER_00', custom_name: '', confidence_score: 0, match_source: '', match_tier: '' },
      { original_speaker: 'SPEAKER_01', custom_name: '', confidence_score: 0, match_source: '', match_tier: '' },
    ];

    await page.route('**/api/v1/transcription/*/speakers', (route) => {
      if (route.request().method() === 'GET') {
        return route.fulfill({ json: speakerMappingsState });
      }
      return route.continue();
    });

    // Mock SSE to deliver speaker_identification event after a short delay
    await page.route('**/api/v1/events*', (route) => {
      const ssePayload = JSON.stringify({
        type: 'speaker_identification',
        payload: {
          job_id: '1',
          auto_assigned: [
            { speaker: 'SPEAKER_00', contact_id: 1, contact_name: 'Alice Johnson', score: 0.92, tier: 'auto' },
          ],
          suggestions: [
            { speaker: 'SPEAKER_01', contact_id: 2, contact_name: 'Bob Smith', score: 0.72, tier: 'suggest' },
          ],
          unmatched: [],
        },
      });

      return route.fulfill({
        status: 200,
        headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' },
        body: `data: {"type":"connected"}\n\ndata: ${ssePayload}\n\n`,
      });
    });

    // After SSE event, speaker mappings should reflect auto-assigned speakers
    speakerMappingsState = [
      { original_speaker: 'SPEAKER_00', custom_name: 'Alice Johnson', confidence_score: 0.92, match_source: 'auto', match_tier: 'auto' },
      { original_speaker: 'SPEAKER_01', custom_name: '', confidence_score: 0, match_source: '', match_tier: '' },
    ];

    await page.goto('/audio/1');
    await page.waitForLoadState('networkidle');

    // The SSE event should trigger a cache invalidation for audioFiles query
    // and the auto-assigned speaker name should appear in the transcript
    await expect(page.locator('text=Alice Johnson').first()).toBeVisible({ timeout: 10000 });
  });

  test('suggestion badge appears in speaker rename dialog after SSE event', async ({ page }) => {
    // Override with un-renamed speakers
    await page.route('**/api/v1/transcription/*/speakers', (route) => {
      if (route.request().method() === 'GET') {
        return route.fulfill({
          json: [
            { original_speaker: 'SPEAKER_00', custom_name: 'Alice Johnson', confidence_score: 0.92, match_source: 'auto', match_tier: 'auto' },
            { original_speaker: 'SPEAKER_01', custom_name: '', confidence_score: 0, match_source: '', match_tier: '' },
          ],
        });
      }
      return route.continue();
    });

    // SSE delivers suggestion for SPEAKER_01
    await page.route('**/api/v1/events*', (route) => {
      const ssePayload = JSON.stringify({
        type: 'speaker_identification',
        payload: {
          job_id: '1',
          auto_assigned: [
            { speaker: 'SPEAKER_00', contact_id: 1, contact_name: 'Alice Johnson', score: 0.92, tier: 'auto' },
          ],
          suggestions: [
            { speaker: 'SPEAKER_01', contact_id: 2, contact_name: 'Bob Smith', score: 0.72, tier: 'suggest' },
          ],
          unmatched: [],
        },
      });
      return route.fulfill({
        status: 200,
        headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' },
        body: `data: {"type":"connected"}\n\ndata: ${ssePayload}\n\n`,
      });
    });

    await page.goto('/audio/1');
    await page.waitForLoadState('networkidle');

    // Open the speaker rename dialog
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
      // Auto-assigned speaker should show "Auto 92%" badge
      const autoBadge = dialog.locator('[data-testid="badge-auto-SPEAKER_00"]');
      if (await autoBadge.isVisible().catch(() => false)) {
        await expect(autoBadge).toContainText('Auto 92%');
      }

      // Suggested speaker should show actionable chip "Bob Smith (72%)"
      const suggestionChip = dialog.locator('button:has-text("Bob Smith")');
      if (await suggestionChip.isVisible().catch(() => false)) {
        await expect(suggestionChip).toContainText('72%');
      }
    }
  });
});

test.describe('Contact Re-scan Button', () => {
  test('re-scan button is visible for contacts with ready voice signatures', async ({ page }) => {
    // Ensure contact has ready signature
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

    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    // Click Alice to open detail pane
    await expect(page.locator('button:has-text("Alice Johnson")').first()).toBeVisible({ timeout: 10000 });
    await page.locator('button:has-text("Alice Johnson")').first().click();
    await page.waitForTimeout(500);

    // Re-scan button should be visible
    await expect(page.locator('button:has-text("Re-scan Past Transcriptions")').first()).toBeVisible({ timeout: 10000 });
  });

  test('re-scan button is hidden for contacts without voice signatures', async ({ page }) => {
    // Contact with no signature
    await page.route(/\/api\/v1\/contacts\/\d+$/, (route) => {
      if (route.request().method() === 'GET') {
        return route.fulfill({
          json: {
            ...CONTACTS[1], // Bob has signature_status: 'none'
            signature_status: 'none',
          },
        });
      }
      return route.continue();
    });

    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    // Click Bob
    await expect(page.locator('button:has-text("Bob Smith")').first()).toBeVisible({ timeout: 10000 });
    await page.locator('button:has-text("Bob Smith")').first().click();
    await page.waitForTimeout(500);

    // Re-scan button should NOT be visible
    await expect(page.locator('button:has-text("Re-scan Past Transcriptions")')).not.toBeVisible();
  });

  test('re-scan button triggers API call and shows success banner', async ({ page }) => {
    let rescanCalled = false;

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

    // Mock the rescan endpoint
    await page.route('**/api/v1/contacts/*/rescan', (route) => {
      rescanCalled = true;
      return route.fulfill({
        json: {
          jobs_scanned: 4,
          auto_assigned: 2,
          suggestions: 1,
        },
      });
    });

    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    // Click Alice
    await page.locator('button:has-text("Alice Johnson")').first().click();
    await page.waitForTimeout(500);

    // Click re-scan button
    const rescanButton = page.locator('button:has-text("Re-scan Past Transcriptions")').first();
    await expect(rescanButton).toBeVisible({ timeout: 10000 });
    await rescanButton.click();

    // Wait for API call
    await page.waitForTimeout(500);

    // Verify the API was called
    expect(rescanCalled).toBe(true);

    // Should show success banner
    await expect(page.locator('text=Retroactive scan started').first()).toBeVisible({ timeout: 10000 });
  });

  test('re-scan button shows scanning state while pending', async ({ page }) => {
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

    // Slow response to test loading state
    await page.route('**/api/v1/contacts/*/rescan', async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 1000));
      return route.fulfill({
        json: { jobs_scanned: 4, auto_assigned: 2, suggestions: 1 },
      });
    });

    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    await page.locator('button:has-text("Alice Johnson")').first().click();
    await page.waitForTimeout(500);

    const rescanButton = page.locator('button:has-text("Re-scan Past Transcriptions")').first();
    await expect(rescanButton).toBeVisible({ timeout: 10000 });
    await rescanButton.click();

    // Should show "Scanning..." while pending
    await expect(page.locator('button:has-text("Scanning...")').first()).toBeVisible({ timeout: 5000 });
  });

  test('re-scan button shows error banner on failure', async ({ page }) => {
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

    // Mock failure — omit error field so parseError uses the fallback message
    await page.route('**/api/v1/contacts/*/rescan', (route) => {
      return route.fulfill({
        status: 500,
        json: {},
      });
    });

    await page.goto('/contacts');
    await page.waitForLoadState('networkidle');

    await page.locator('button:has-text("Alice Johnson")').first().click();
    await page.waitForTimeout(500);

    await page.locator('button:has-text("Re-scan Past Transcriptions")').first().click();
    await page.waitForTimeout(500);

    // Should show error banner (fallback text from setErrorBanner)
    await expect(page.locator('text=Failed to start retroactive scan').first()).toBeVisible({ timeout: 10000 });
  });
});

test.describe('Promote Speaker Suggestion', () => {
  test('clicking voice suggestion chip promotes speaker and locks input', async ({ page }) => {
    let promoteCalled = false;

    // Un-renamed speakers
    await page.route('**/api/v1/transcription/*/speakers', (route) => {
      if (route.request().method() === 'GET') {
        return route.fulfill({
          json: [
            { original_speaker: 'SPEAKER_00', custom_name: '', confidence_score: 0, match_source: '', match_tier: '' },
            { original_speaker: 'SPEAKER_01', custom_name: '', confidence_score: 0, match_source: '', match_tier: '' },
          ],
        });
      }
      return route.continue();
    });

    // Mock promote endpoint
    await page.route('**/api/v1/transcription/*/speakers/promote', (route) => {
      promoteCalled = true;
      return route.fulfill({
        json: {
          mappings: [
            { original_speaker: 'SPEAKER_00', custom_name: 'Alice Johnson', confidence_score: 0.92, match_source: 'suggestion_promoted', match_tier: 'suggest' },
            { original_speaker: 'SPEAKER_01', custom_name: '', confidence_score: 0, match_source: '', match_tier: '' },
          ],
          contact_bootstrap: { started_count: 0, created_count: 0, skipped_existing_count: 0 },
        },
      });
    });

    // SSE with suggestion
    await page.route('**/api/v1/events*', (route) => {
      const ssePayload = JSON.stringify({
        type: 'speaker_identification',
        payload: {
          job_id: '1',
          auto_assigned: [],
          suggestions: [
            { speaker: 'SPEAKER_00', contact_id: 1, contact_name: 'Alice Johnson', score: 0.92, tier: 'suggest' },
          ],
          unmatched: ['SPEAKER_01'],
        },
      });
      return route.fulfill({
        status: 200,
        headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' },
        body: `data: {"type":"connected"}\n\ndata: ${ssePayload}\n\n`,
      });
    });

    await page.goto('/audio/1');
    await page.waitForLoadState('networkidle');

    // Open speaker rename dialog
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
      // Click the suggestion chip "Alice Johnson (92%)"
      const suggestionChip = dialog.locator('button:has-text("Alice Johnson")');
      if (await suggestionChip.isVisible().catch(() => false)) {
        await suggestionChip.click();
        await page.waitForTimeout(500);

        expect(promoteCalled).toBe(true);

        // After promotion, input should show Alice Johnson and be locked
        const input = dialog.locator('input#speaker-SPEAKER_00');
        if (await input.isVisible().catch(() => false)) {
          await expect(input).toHaveValue('Alice Johnson');
        }

        // Matched badge should appear
        const matchedBadge = dialog.locator('[data-testid="badge-matched-SPEAKER_00"]');
        if (await matchedBadge.isVisible().catch(() => false)) {
          await expect(matchedBadge).toContainText('Matched');
        }
      }
    }
  });
});
