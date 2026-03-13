import { type Locator, type Page } from '@playwright/test';

export class DashboardPage {
  readonly page: Page;
  readonly heading: Locator;
  readonly audioTable: Locator;
  readonly audioRows: Locator;
  readonly searchInput: Locator;
  readonly addAudioButton: Locator;

  constructor(page: Page) {
    this.page = page;
    this.heading = page.locator('h1, h2').first();
    this.audioTable = page.locator('table, [role="table"], [class*="audio"]').first();
    this.audioRows = page.locator('tr, [role="row"]');
    this.searchInput = page.locator('input[type="search"], input[placeholder*="earch"], input[placeholder*="ilter"]');
    this.addAudioButton = page.locator('button:has-text("Add"), button[aria-label*="add"], button[aria-label*="upload"]').first();
  }

  async goto() {
    await this.page.goto('/');
    await this.page.waitForLoadState('networkidle');
  }

  async searchFiles(query: string) {
    await this.searchInput.fill(query);
    await this.page.waitForTimeout(300); // debounce
  }

  async clearSearch() {
    await this.searchInput.clear();
    await this.page.waitForTimeout(300);
  }

  async clickAudioFile(title: string) {
    await this.page.locator(`text=${title}`).click();
  }

  getAudioRow(title: string): Locator {
    return this.page.locator(`tr:has-text("${title}"), [role="row"]:has-text("${title}")`);
  }
}
