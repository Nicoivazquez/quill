import { contextBridge, ipcRenderer } from "electron";

type DesktopBridge = {
  selectFolder: () => Promise<string | null>;
};

const bridge: DesktopBridge = {
  async selectFolder() {
    const selectedPath = await ipcRenderer.invoke("desktop:select-folder");
    return typeof selectedPath === "string" && selectedPath.length > 0 ? selectedPath : null;
  },
};

contextBridge.exposeInMainWorld("scriberrDesktop", bridge);
