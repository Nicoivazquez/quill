export type ScriberrDesktopBridge = {
  selectFolder: (options?: {
    title?: string;
    defaultPath?: string;
  }) => Promise<string | null>;
};

declare global {
  interface Window {
    scriberrDesktop?: ScriberrDesktopBridge;
  }
}

export {};
