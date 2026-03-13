import { type Locator, type Page } from '@playwright/test';

export class ContactsPage {
  readonly page: Page;
  readonly heading: Locator;
  readonly contactList: Locator;
  readonly searchInput: Locator;
  readonly addContactButton: Locator;
  readonly contactNameInput: Locator;
  readonly contactEmailInput: Locator;
  readonly contactPhoneInput: Locator;
  readonly saveButton: Locator;
  readonly deleteButton: Locator;
  readonly reindexButton: Locator;

  constructor(page: Page) {
    this.page = page;
    this.heading = page.locator('h1:has-text("Contacts"), h2:has-text("Contacts")').first();
    this.contactList = page.locator('[class*="contact-list"], [class*="ContactList"], ul, [role="list"]').first();
    this.searchInput = page.locator('input[type="search"], input[placeholder*="earch"]');
    this.addContactButton = page.locator('button:has-text("Add"), button:has-text("New"), button:has-text("Create")').first();
    this.contactNameInput = page.locator('input[name="name"], input[placeholder*="ame"]').first();
    this.contactEmailInput = page.locator('input[name="email"], input[placeholder*="mail"], input[type="email"]').first();
    this.contactPhoneInput = page.locator('input[name="phone"], input[placeholder*="hone"], input[type="tel"]').first();
    this.saveButton = page.locator('button:has-text("Save"), button:has-text("Update"), button[type="submit"]').first();
    this.deleteButton = page.locator('button:has-text("Delete"), button[aria-label="Delete"]').first();
    this.reindexButton = page.locator('button:has-text("Reindex"), button:has-text("Sync")').first();
  }

  async goto() {
    await this.page.goto('/contacts');
    await this.page.waitForLoadState('networkidle');
  }

  async searchContacts(query: string) {
    await this.searchInput.fill(query);
    await this.page.waitForTimeout(300);
  }

  async clickContact(name: string) {
    await this.page.locator(`text=${name}`).first().click();
  }

  getContactItem(name: string): Locator {
    return this.page.locator(`text=${name}`).first();
  }
}
