import { useCallback, useEffect, useMemo, useState } from "react";
import { AlertCircle, FolderPlus, Loader2, PauseCircle, PlayCircle, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useAuth } from "@/features/auth/hooks/useAuth";

type WatchedFolder = {
  id: number;
  path: string;
  recursive: boolean;
  enabled: boolean;
  active: boolean;
  last_runtime_error?: string;
  last_imported_at?: string;
  last_imported_file?: string;
};

type WatchedFoldersResponse = {
  folders: WatchedFolder[];
};

async function parseAPIError(response: Response): Promise<string> {
  try {
    const data = (await response.json()) as { error?: string };
    if (data.error) {
      return data.error;
    }
  } catch {
    // ignore parse failures and use fallback
  }
  return `Request failed with status ${response.status}`;
}

function formatTimestamp(value?: string): string {
  if (!value) {
    return "";
  }
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) {
    return "";
  }
  return parsed.toLocaleString();
}

export function AutoImportSettingsTab() {
  const { getAuthHeaders } = useAuth();
  const [folders, setFolders] = useState<WatchedFolder[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [busyFolderID, setBusyFolderID] = useState<number | null>(null);

  const isDesktopApp = useMemo(() => {
    return typeof window !== "undefined" && typeof window.scriberrDesktop?.selectFolder === "function";
  }, []);

  const loadFolders = useCallback(async () => {
    setError(null);
    try {
      const response = await fetch("/api/v1/watch-folders", {
        headers: { ...getAuthHeaders() },
      });
      if (!response.ok) {
        throw new Error(await parseAPIError(response));
      }

      const data = (await response.json()) as WatchedFoldersResponse;
      setFolders(data.folders ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load watched folders");
    } finally {
      setLoading(false);
    }
  }, [getAuthHeaders]);

  useEffect(() => {
    loadFolders();
  }, [loadFolders]);

  const addFolder = async () => {
    if (!window.scriberrDesktop?.selectFolder) {
      return;
    }

    setError(null);
    setCreating(true);
    try {
      const selectedPath = await window.scriberrDesktop.selectFolder();
      if (!selectedPath) {
        return;
      }

      const response = await fetch("/api/v1/watch-folders", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...getAuthHeaders(),
        },
        body: JSON.stringify({
          path: selectedPath,
          recursive: true,
          enabled: true,
        }),
      });

      if (!response.ok) {
        throw new Error(await parseAPIError(response));
      }

      await loadFolders();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to add watched folder");
    } finally {
      setCreating(false);
    }
  };

  const toggleFolder = async (folder: WatchedFolder) => {
    setError(null);
    setBusyFolderID(folder.id);
    try {
      const response = await fetch(`/api/v1/watch-folders/${folder.id}`, {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
          ...getAuthHeaders(),
        },
        body: JSON.stringify({ enabled: !folder.enabled }),
      });

      if (!response.ok) {
        throw new Error(await parseAPIError(response));
      }

      await loadFolders();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to update watched folder");
    } finally {
      setBusyFolderID(null);
    }
  };

  const deleteFolder = async (folder: WatchedFolder) => {
    if (!window.confirm(`Stop watching and remove folder?\n\n${folder.path}`)) {
      return;
    }

    setError(null);
    setBusyFolderID(folder.id);
    try {
      const response = await fetch(`/api/v1/watch-folders/${folder.id}`, {
        method: "DELETE",
        headers: {
          ...getAuthHeaders(),
        },
      });

      if (!response.ok && response.status !== 204) {
        throw new Error(await parseAPIError(response));
      }

      await loadFolders();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete watched folder");
    } finally {
      setBusyFolderID(null);
    }
  };

  return (
    <div className="space-y-6">
      <div className="bg-[var(--bg-main)]/50 rounded-[var(--radius-card)] shadow-sm border border-[var(--border-subtle)] p-6 space-y-4">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h2 className="text-xl font-bold text-[var(--text-primary)]">Auto Import Folders</h2>
            <p className="text-[var(--text-secondary)] mt-1">
              Choose folders in the desktop app and Scriberr will automatically import new audio files.
            </p>
          </div>

          <Button
            variant="brand"
            onClick={addFolder}
            disabled={!isDesktopApp || creating}
            className="sm:min-w-44"
          >
            {creating ? <Loader2 className="h-4 w-4 animate-spin" /> : <FolderPlus className="h-4 w-4" />}
            {creating ? "Adding..." : "Add Folder"}
          </Button>
        </div>

        {!isDesktopApp && (
          <div className="text-sm rounded-md border border-[var(--warning-solid)]/30 bg-[var(--warning-translucent)] px-3 py-2 text-[var(--warning-solid)]">
            Folder picking is available in the Electron desktop app.
          </div>
        )}

        {error && (
          <div className="text-sm rounded-md border border-[var(--error)]/30 bg-[var(--error)]/10 px-3 py-2 text-[var(--error)] flex items-start gap-2">
            <AlertCircle className="h-4 w-4 mt-0.5 shrink-0" />
            <span>{error}</span>
          </div>
        )}
      </div>

      <div className="bg-[var(--bg-main)]/50 rounded-[var(--radius-card)] shadow-sm border border-[var(--border-subtle)] overflow-hidden">
        {loading ? (
          <div className="p-6 flex items-center gap-3 text-[var(--text-secondary)]">
            <Loader2 className="h-4 w-4 animate-spin" />
            Loading watched folders...
          </div>
        ) : folders.length === 0 ? (
          <div className="p-6 text-[var(--text-secondary)]">
            No folders configured yet. Add a folder to start automatic imports.
          </div>
        ) : (
          <ul className="divide-y divide-[var(--border-subtle)]">
            {folders.map((folder) => {
              const isBusy = busyFolderID === folder.id;
              const statusLabel = folder.enabled ? (folder.active ? "Watching" : "Needs attention") : "Paused";
              const statusClass = folder.enabled
                ? folder.active
                  ? "text-emerald-600 bg-emerald-500/10 border-emerald-500/30"
                  : "text-amber-600 bg-amber-500/10 border-amber-500/30"
                : "text-[var(--text-secondary)] bg-[var(--bg-main)] border-[var(--border-subtle)]";

              return (
                <li key={folder.id} className="p-4 sm:p-5 space-y-3">
                  <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                    <div className="space-y-2 min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className={`text-xs font-medium rounded-full border px-2 py-1 ${statusClass}`}>
                          {statusLabel}
                        </span>
                        {folder.recursive && (
                          <span className="text-xs rounded-full border px-2 py-1 border-[var(--border-subtle)] text-[var(--text-secondary)]">
                            Recursive
                          </span>
                        )}
                      </div>
                      <p className="text-sm font-mono break-all text-[var(--text-primary)]">{folder.path}</p>
                      {folder.last_runtime_error && (
                        <p className="text-sm text-[var(--error)]">{folder.last_runtime_error}</p>
                      )}
                      {folder.last_imported_file && (
                        <p className="text-xs text-[var(--text-secondary)]">
                          Last import: <span className="font-mono break-all">{folder.last_imported_file}</span>
                          {folder.last_imported_at && ` at ${formatTimestamp(folder.last_imported_at)}`}
                        </p>
                      )}
                    </div>

                    <div className="flex items-center gap-2">
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={isBusy}
                        onClick={() => toggleFolder(folder)}
                      >
                        {isBusy ? (
                          <Loader2 className="h-4 w-4 animate-spin" />
                        ) : folder.enabled ? (
                          <PauseCircle className="h-4 w-4" />
                        ) : (
                          <PlayCircle className="h-4 w-4" />
                        )}
                        {folder.enabled ? "Pause" : "Resume"}
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={isBusy}
                        onClick={() => deleteFolder(folder)}
                        className="text-[var(--error)] hover:text-[var(--error)]"
                      >
                        <Trash2 className="h-4 w-4" />
                        Remove
                      </Button>
                    </div>
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </div>
  );
}
