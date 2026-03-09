export type QuillDesktopBridge = {
  selectFolder: (options?: {
    title?: string;
    defaultPath?: string;
  }) => Promise<string | null>;
};

declare global {
  interface Window {
    quillDesktop?: QuillDesktopBridge;
  }
}

export {};
