import { contextBridge, ipcRenderer } from "electron";

type DesktopBridge = {
  selectFolder: (options?: {
    title?: string;
    defaultPath?: string;
  }) => Promise<string | null>;
};

const bridge: DesktopBridge = {
  async selectFolder(options) {
    const selectedPath = await ipcRenderer.invoke("desktop:select-folder", options ?? null);
    return typeof selectedPath === "string" && selectedPath.length > 0 ? selectedPath : null;
  },
};

contextBridge.exposeInMainWorld("scriberrDesktop", bridge);
