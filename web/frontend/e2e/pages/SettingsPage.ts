import { type Locator, type Page } from '@playwright/test';

export class SettingsPage {
  readonly page: Page;
  readonly heading: Locator;
  readonly tabs: Locator;
  readonly cloudProvidersTab: Locator;
  readonly transcriptionTab: Locator;
  readonly accountTab: Locator;
  readonly apiKeysTab: Locator;
  readonly llmTab: Locator;
  readonly summaryTab: Locator;
  readonly vaultsTab: Locator;

  constructor(page: Page) {
    this.page = page;
    this.heading = page.locator('h1:has-text("Settings"), h2:has-text("Settings")').first();
    this.tabs = page.locator('[role="tablist"] [role="tab"], button[role="tab"]');
    this.cloudProvidersTab = page.locator('[role="tab"]:has-text("Cloud"), [role="tab"]:has-text("Provider")').first();
    this.transcriptionTab = page.locator('[role="tab"]:has-text("Transcription")').first();
    this.accountTab = page.locator('[role="tab"]:has-text("Account")').first();
    this.apiKeysTab = page.locator('[role="tab"]:has-text("API")').first();
    this.llmTab = page.locator('[role="tab"]:has-text("LLM")').first();
    this.summaryTab = page.locator('[role="tab"]:has-text("Summar")').first();
    this.vaultsTab = page.locator('[role="tab"]:has-text("Vault")').first();
  }

  async goto() {
    await this.page.goto('/settings');
    await this.page.waitForLoadState('networkidle');
  }

  async selectTab(name: string) {
    await this.page.locator(`[role="tab"]:has-text("${name}")`).click();
    await this.page.waitForTimeout(200);
  }

  getTabPanel(): Locator {
    return this.page.locator('[role="tabpanel"]').first();
  }
}
