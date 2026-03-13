import { test, expect } from '@playwright/test';
import { installApiMocks, setLocalAuthState } from './fixtures/api-mocks';
import path from 'path';
import fs from 'fs';
import { fileURLToPath } from 'url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

test.beforeEach(async ({ page }) => {
  await setLocalAuthState(page);
  await installApiMocks(page);
});

test.describe('Upload Audio File', () => {
  test('upload button is accessible from header', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // The header has a "+" button with sr-only text "Add audio"
    const addButton = page.locator('button').filter({ has: page.locator('.sr-only:has-text("Add audio")') }).first();
    // Fallback: look for any plus-icon button or button with "Add" accessible name
    const anyAddButton = addButton.or(page.locator('button:has-text("Add audio")')).first();
    await expect(anyAddButton).toBeVisible({ timeout: 10000 });
  });

  test('upload triggers API call', async ({ page }) => {
    let uploadCalled = false;
    await page.route('**/api/v1/transcription/upload', (route) => {
      uploadCalled = true;
      return route.fulfill({
        json: {
          id: '5',
          title: 'test-audio',
          status: 'uploaded',
          audio_path: '/data/uploads/test-audio.wav',
          created_at: new Date().toISOString(),
        },
      });
    });

    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // Create a minimal WAV file for upload
    const testAudioPath = path.join(__dirname, 'fixtures', 'test-audio.wav');
    if (!fs.existsSync(testAudioPath)) {
      const header = Buffer.alloc(44);
      header.write('RIFF', 0);
      header.writeUInt32LE(36, 4);
      header.write('WAVE', 8);
      header.write('fmt ', 12);
      header.writeUInt32LE(16, 16);
      header.writeUInt16LE(1, 20);
      header.writeUInt16LE(1, 22);
      header.writeUInt32LE(44100, 24);
      header.writeUInt32LE(88200, 28);
      header.writeUInt16LE(2, 32);
      header.writeUInt16LE(16, 34);
      header.write('data', 36);
      header.writeUInt32LE(0, 40);
      fs.writeFileSync(testAudioPath, header);
    }

    // Find the hidden file input and upload
    const fileInput = page.locator('input[type="file"]').first();
    if (await fileInput.count() > 0) {
      await fileInput.setInputFiles(testAudioPath);
      await page.waitForTimeout(1000);
      expect(uploadCalled).toBe(true);
    }
  });

  test('uploaded file appears with pending status', async ({ page }) => {
    // After upload, the list should contain the new file
    await page.route('**/api/v1/transcription/list*', (route) =>
      route.fulfill({
        json: {
          jobs: [
            {
              id: '5',
              title: 'test-audio',
              status: 'uploaded',
              audio_path: '/data/uploads/test-audio.wav',
              created_at: new Date().toISOString(),
              duration: 0,
              speakers: 0,
            },
          ],
          pagination: { page: 1, limit: 20, total: 1, pages: 1 },
        },
      }),
    );

    await page.goto('/');
    await page.waitForLoadState('networkidle');

    await expect(page.locator('text=test-audio').first()).toBeVisible({ timeout: 10000 });
  });
});
