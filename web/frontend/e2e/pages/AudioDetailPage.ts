import { type Locator, type Page } from '@playwright/test';

export class AudioDetailPage {
  readonly page: Page;
  readonly title: Locator;
  readonly transcriptSection: Locator;
  readonly transcriptSegments: Locator;
  readonly speakerLabels: Locator;
  readonly downloadButton: Locator;
  readonly backButton: Locator;

  constructor(page: Page) {
    this.page = page;
    this.title = page.locator('h1, h2, [class*="title"]').first();
    this.transcriptSection = page.locator('[class*="transcript"], [class*="Transcript"]').first();
    this.transcriptSegments = page.locator('[class*="segment"], [data-speaker]');
    this.speakerLabels = page.locator('[class*="speaker"], [class*="Speaker"]');
    this.downloadButton = page.locator('button:has-text("Download"), button:has-text("Export")').first();
    this.backButton = page.locator('a[href="/"], button:has-text("Back"), [aria-label="Back"]').first();
  }

  async goto(audioId: number) {
    await this.page.goto(`/audio/${audioId}`);
    await this.page.waitForLoadState('networkidle');
  }

  async clickSpeaker(speakerName: string) {
    await this.page.locator(`text=${speakerName}`).first().click();
  }

  async getTranscriptText(): Promise<string> {
    return (await this.transcriptSection.textContent()) ?? '';
  }
}
