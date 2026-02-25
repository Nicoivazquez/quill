export type ScriberrDesktopBridge = {
  selectFolder: () => Promise<string | null>;
};

declare global {
  interface Window {
    scriberrDesktop?: ScriberrDesktopBridge;
  }
}

export {};
