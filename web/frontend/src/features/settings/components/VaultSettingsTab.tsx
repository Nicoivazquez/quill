import { useCallback, useEffect, useMemo, useState } from "react";
import { AlertCircle, CheckCircle2, Clapperboard, FolderOpen, HardDrive, Loader2, BookMarked, RefreshCw } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { useAuth } from "@/features/auth/hooks/useAuth";

type Vault = {
  id: number;
  name: string;
  path: string;
  is_active: boolean;
};

type SetupStateResponse = {
  vaults: Vault[];
  active_vault?: Vault;
};

function getDesktopBridge() {
  if (typeof window === "undefined") {
    return undefined;
  }
  return window.quillDesktop;
}

async function parseAPIError(response: Response): Promise<string> {
  try {
    const payload = (await response.json()) as { error?: string };
    if (payload.error) {
      return payload.error;
    }
  } catch {
    // ignore parse errors
  }
  return `Request failed with status ${response.status}`;
}

export function VaultSettingsTab() {
  const { getAuthHeaders } = useAuth();
  const queryClient = useQueryClient();
  const [vaults, setVaults] = useState<Vault[]>([]);
  const [activeVaultID, setActiveVaultID] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [connecting, setConnecting] = useState(false);
  const [rehydrating, setRehydrating] = useState(false);
  const [busyVaultID, setBusyVaultID] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);

  // Obsidian Bridge state
  const [obsVaultPath, setObsVaultPath] = useState("");
  const [obsLoading, setObsLoading] = useState(true);
  const [obsSaving, setObsSaving] = useState(false);
  const [obsSyncing, setObsSyncing] = useState(false);
  const [obsError, setObsError] = useState<string | null>(null);
  const [obsSuccess, setObsSuccess] = useState<string | null>(null);

  // OpenClaw Bridge state
  const [ocDropDir, setOcDropDir] = useState("");
  const [ocLoading, setOcLoading] = useState(true);
  const [ocSaving, setOcSaving] = useState(false);
  const [ocIngesting, setOcIngesting] = useState(false);
  const [ocError, setOcError] = useState<string | null>(null);
  const [ocSuccess, setOcSuccess] = useState<string | null>(null);

  const isDesktopApp = useMemo(() => {
    return typeof getDesktopBridge()?.selectFolder === "function";
  }, []);

  const loadVaults = useCallback(async () => {
    setError(null);
    try {
      const response = await fetch("/api/v1/setup/state", {
        headers: { ...getAuthHeaders() },
      });
      if (!response.ok) {
        throw new Error(await parseAPIError(response));
      }

      const payload = (await response.json()) as SetupStateResponse;
      const resolvedVaults = payload.vaults ?? [];
      setVaults(resolvedVaults);

      const activeVault = payload.active_vault ?? resolvedVaults.find((vault) => vault.is_active);
      setActiveVaultID(activeVault?.id ?? null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load vaults");
    } finally {
      setLoading(false);
    }
  }, [getAuthHeaders]);

  useEffect(() => {
    void loadVaults();
  }, [loadVaults]);

  // Load Obsidian config
  const loadObsidianConfig = useCallback(async () => {
    try {
      const response = await fetch("/api/v1/obsidian/config", {
        headers: { ...getAuthHeaders() },
      });
      if (!response.ok) return;
      const payload = (await response.json()) as { vault_path?: string };
      setObsVaultPath(payload.vault_path ?? "");
    } catch {
      // ignore – non-critical
    } finally {
      setObsLoading(false);
    }
  }, [getAuthHeaders]);

  useEffect(() => {
    void loadObsidianConfig();
  }, [loadObsidianConfig]);

  const saveObsidianConfig = useCallback(async () => {
    setObsSaving(true);
    setObsError(null);
    setObsSuccess(null);
    try {
      const response = await fetch("/api/v1/obsidian/config", {
        method: "POST",
        headers: { "Content-Type": "application/json", ...getAuthHeaders() },
        body: JSON.stringify({ vault_path: obsVaultPath }),
      });
      if (!response.ok) {
        throw new Error(await parseAPIError(response));
      }
      setObsSuccess("Obsidian vault path saved.");
    } catch (err) {
      setObsError(err instanceof Error ? err.message : "Failed to save Obsidian config");
    } finally {
      setObsSaving(false);
    }
  }, [obsVaultPath, getAuthHeaders]);

  const syncAllToObsidian = useCallback(async () => {
    setObsSyncing(true);
    setObsError(null);
    setObsSuccess(null);
    try {
      const response = await fetch("/api/v1/obsidian/sync-all", {
        method: "POST",
        headers: { ...getAuthHeaders() },
      });
      if (!response.ok) {
        throw new Error(await parseAPIError(response));
      }
      const payload = (await response.json()) as { synced?: number; failed?: number; total?: number };
      const synced = payload.synced ?? 0;
      const failed = payload.failed ?? 0;
      setObsSuccess(`Synced ${synced} transcript${synced === 1 ? "" : "s"} to Obsidian.${failed > 0 ? ` ${failed} failed.` : ""}`);
    } catch (err) {
      setObsError(err instanceof Error ? err.message : "Failed to sync to Obsidian");
    } finally {
      setObsSyncing(false);
    }
  }, [getAuthHeaders]);

  const browseObsidianVault = useCallback(async () => {
    const desktopBridge = getDesktopBridge();
    if (!desktopBridge?.selectFolder) {
      setObsError("Folder picker is available in the desktop app.");
      return;
    }
    try {
      const selectedPath = await desktopBridge.selectFolder({
        title: "Select Obsidian Vault Folder",
      });
      if (selectedPath) {
        setObsVaultPath(selectedPath);
      }
    } catch (err) {
      setObsError(err instanceof Error ? err.message : "Failed to select folder");
    }
  }, []);

  // Load OpenClaw config
  const loadOpenClawConfig = useCallback(async () => {
    try {
      const response = await fetch("/api/v1/openclaw/config", {
        headers: { ...getAuthHeaders() },
      });
      if (!response.ok) return;
      const payload = (await response.json()) as { drop_dir?: string };
      setOcDropDir(payload.drop_dir ?? "");
    } catch {
      // ignore – non-critical
    } finally {
      setOcLoading(false);
    }
  }, [getAuthHeaders]);

  useEffect(() => {
    void loadOpenClawConfig();
  }, [loadOpenClawConfig]);

  const saveOpenClawConfig = useCallback(async () => {
    setOcSaving(true);
    setOcError(null);
    setOcSuccess(null);
    try {
      const response = await fetch("/api/v1/openclaw/config", {
        method: "POST",
        headers: { "Content-Type": "application/json", ...getAuthHeaders() },
        body: JSON.stringify({ drop_dir: ocDropDir }),
      });
      if (!response.ok) {
        throw new Error(await parseAPIError(response));
      }
      setOcSuccess("OpenClaw drop folder path saved.");
    } catch (err) {
      setOcError(err instanceof Error ? err.message : "Failed to save OpenClaw config");
    } finally {
      setOcSaving(false);
    }
  }, [ocDropDir, getAuthHeaders]);

  const ingestDropFolder = useCallback(async () => {
    setOcIngesting(true);
    setOcError(null);
    setOcSuccess(null);
    try {
      const response = await fetch("/api/v1/openclaw/ingest-drop", {
        method: "POST",
        headers: { ...getAuthHeaders() },
      });
      if (!response.ok) {
        throw new Error(await parseAPIError(response));
      }
      const payload = (await response.json()) as { ingested?: string[]; skipped?: string[] };
      const ingested = payload.ingested ?? [];
      const skipped = payload.skipped ?? [];
      setOcSuccess(`Ingested ${ingested.length} file${ingested.length === 1 ? "" : "s"}.${skipped.length > 0 ? ` ${skipped.length} skipped.` : ""}`);
    } catch (err) {
      setOcError(err instanceof Error ? err.message : "Failed to ingest drop folder");
    } finally {
      setOcIngesting(false);
    }
  }, [getAuthHeaders]);

  const browseOpenClawDrop = useCallback(async () => {
    const desktopBridge = getDesktopBridge();
    if (!desktopBridge?.selectFolder) {
      setOcError("Folder picker is available in the desktop app.");
      return;
    }
    try {
      const selectedPath = await desktopBridge.selectFolder({
        title: "Select OpenClaw Drop Folder",
      });
      if (selectedPath) {
        setOcDropDir(selectedPath);
      }
    } catch (err) {
      setOcError(err instanceof Error ? err.message : "Failed to select folder");
    }
  }, []);

  const activateVault = useCallback(
    async (vaultID: number) => {
      setBusyVaultID(vaultID);
      setError(null);
      setSuccess(null);
      try {
        const response = await fetch(`/api/v1/vaults/${vaultID}/activate`, {
          method: "POST",
          headers: { ...getAuthHeaders() },
        });
        if (!response.ok) {
          throw new Error(await parseAPIError(response));
        }
        await loadVaults();
        // Invalidate all transcript/folder/contact caches so the UI reflects the new vault.
        await queryClient.invalidateQueries({ queryKey: ["audioFiles"] });
        await queryClient.invalidateQueries({ queryKey: ["folders"] });
        await queryClient.invalidateQueries({ queryKey: ["contacts"] });
        setSuccess("Active vault updated.");
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to activate vault");
      } finally {
        setBusyVaultID(null);
      }
    },
    [getAuthHeaders, loadVaults, queryClient],
  );

  const connectExistingVault = async () => {
    const desktopBridge = getDesktopBridge();
    if (!desktopBridge?.selectFolder) {
      setError("Vault picker is available in the desktop app.");
      return;
    }

    setConnecting(true);
    setError(null);
    setSuccess(null);
    try {
      const selectedPath = await desktopBridge.selectFolder({
        title: "Select Existing Vault Folder",
      });
      if (!selectedPath) {
        return;
      }

      const knownVault = vaults.find((vault) => vault.path === selectedPath);
      if (knownVault) {
        await activateVault(knownVault.id);
        return;
      }

      const response = await fetch("/api/v1/vaults", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...getAuthHeaders(),
        },
        body: JSON.stringify({
          path: selectedPath,
          mode: "existing",
          activate: true,
        }),
      });
      if (!response.ok) {
        throw new Error(await parseAPIError(response));
      }

      await loadVaults();
      setSuccess("Existing vault connected.");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to connect existing vault");
    } finally {
      setConnecting(false);
    }
  };

  const rehydrateActiveVault = useCallback(async () => {
    if (!activeVaultID) {
      setError("No active vault selected.");
      return;
    }

    setRehydrating(true);
    setError(null);
    setSuccess(null);
    try {
      const response = await fetch(`/api/v1/vaults/${activeVaultID}/rehydrate`, {
        method: "POST",
        headers: { ...getAuthHeaders() },
      });
      if (!response.ok) {
        throw new Error(await parseAPIError(response));
      }

      const payload = (await response.json()) as { recovered_jobs?: number };
      const recoveredJobs = typeof payload.recovered_jobs === "number" ? payload.recovered_jobs : 0;
      setSuccess(`Vault rescan complete. Recovered ${recoveredJobs} job${recoveredJobs === 1 ? "" : "s"}.`);
      await loadVaults();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to rescan vault");
    } finally {
      setRehydrating(false);
    }
  }, [activeVaultID, getAuthHeaders, loadVaults]);

  const activeVault = vaults.find((vault) => vault.id === activeVaultID) ?? null;

  return (
    <div className="space-y-6">
      <div className="bg-[var(--bg-main)]/50 rounded-[var(--radius-card)] shadow-sm border border-[var(--border-subtle)] p-6 space-y-4">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h2 className="text-xl font-bold text-[var(--text-primary)]">Vaults</h2>
            <p className="text-[var(--text-secondary)] mt-1">
              Connect an existing vault folder and switch active vaults from settings.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Button variant="outline" disabled={!activeVaultID || rehydrating} onClick={rehydrateActiveVault}>
              {rehydrating ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
              {rehydrating ? "Rescanning..." : "Rescan Active Vault"}
            </Button>
            <Button variant="brand" disabled={!isDesktopApp || connecting} onClick={connectExistingVault}>
              {connecting ? <Loader2 className="h-4 w-4 animate-spin" /> : <FolderOpen className="h-4 w-4" />}
              {connecting ? "Connecting..." : "Connect Existing Vault"}
            </Button>
          </div>
        </div>

        {!isDesktopApp && (
          <div className="text-sm rounded-md border border-[var(--warning-solid)]/30 bg-[var(--warning-translucent)] px-3 py-2 text-[var(--warning-solid)]">
            Vault folder picking is available in the Electron desktop app.
          </div>
        )}

        {activeVault && (
          <div className="text-sm rounded-md border border-[var(--border-subtle)] bg-[var(--bg-card)] px-3 py-2">
            <div className="flex items-center gap-2 text-[var(--brand-solid)] font-medium">
              <HardDrive className="h-4 w-4" />
              Active Vault
            </div>
            <p className="mt-1 text-[var(--text-primary)]">{activeVault.name}</p>
            <p className="text-xs text-[var(--text-secondary)] break-all">{activeVault.path}</p>
          </div>
        )}

        {error && (
          <div className="text-sm rounded-md border border-[var(--error)]/30 bg-[var(--error)]/10 px-3 py-2 text-[var(--error)] flex items-start gap-2">
            <AlertCircle className="h-4 w-4 mt-0.5 shrink-0" />
            <span>{error}</span>
          </div>
        )}
        {success && (
          <div className="text-sm rounded-md border border-[var(--success)]/30 bg-[var(--success)]/10 px-3 py-2 text-[var(--success)] flex items-start gap-2">
            <CheckCircle2 className="h-4 w-4 mt-0.5 shrink-0" />
            <span>{success}</span>
          </div>
        )}
      </div>

      <div className="bg-[var(--bg-main)]/50 rounded-[var(--radius-card)] shadow-sm border border-[var(--border-subtle)] overflow-hidden">
        {loading ? (
          <div className="p-6 flex items-center gap-3 text-[var(--text-secondary)]">
            <Loader2 className="h-4 w-4 animate-spin" />
            Loading vaults...
          </div>
        ) : vaults.length === 0 ? (
          <div className="p-6 text-[var(--text-secondary)]">
            No vaults connected yet.
          </div>
        ) : (
          <ul className="divide-y divide-[var(--border-subtle)]">
            {vaults.map((vault) => {
              const isActive = vault.id === activeVaultID;
              const isBusy = busyVaultID === vault.id;

              return (
                <li key={vault.id} className="p-4 sm:p-5 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                  <div className="min-w-0">
                    <p className="text-sm font-medium text-[var(--text-primary)]">{vault.name}</p>
                    <p className="text-xs text-[var(--text-secondary)] break-all">{vault.path}</p>
                  </div>
                  <Button
                    variant={isActive ? "outline" : "brand"}
                    size="sm"
                    disabled={isActive || isBusy}
                    onClick={() => void activateVault(vault.id)}
                  >
                    {isBusy ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
                    {isActive ? "Active" : "Use This Vault"}
                  </Button>
                </li>
              );
            })}
          </ul>
        )}
      </div>

      {/* Obsidian Bridge */}
      <div className="bg-[var(--bg-main)]/50 rounded-[var(--radius-card)] shadow-sm border border-[var(--border-subtle)] p-6 space-y-4">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <div className="flex items-center gap-2">
              <BookMarked className="h-5 w-5 text-[var(--text-primary)]" />
              <h2 className="text-xl font-bold text-[var(--text-primary)]">Obsidian Bridge</h2>
            </div>
            <p className="text-[var(--text-secondary)] mt-1">
              Publish completed transcripts to your Obsidian vault with quill-id frontmatter for deterministic updates.
            </p>
          </div>
          <Button
            variant="outline"
            disabled={!obsVaultPath || obsSyncing}
            onClick={() => void syncAllToObsidian()}
          >
            {obsSyncing ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
            {obsSyncing ? "Syncing..." : "Sync All to Obsidian"}
          </Button>
        </div>

        {obsLoading ? (
          <div className="flex items-center gap-3 text-[var(--text-secondary)]">
            <Loader2 className="h-4 w-4 animate-spin" />
            Loading Obsidian config...
          </div>
        ) : (
          <div className="flex flex-col sm:flex-row gap-3">
            <div className="flex-1">
              <label htmlFor="obs-vault-path" className="block text-sm font-medium text-[var(--text-secondary)] mb-1">
                Obsidian Vault Path
              </label>
              <div className="flex gap-2">
                <input
                  id="obs-vault-path"
                  type="text"
                  value={obsVaultPath}
                  onChange={(e) => setObsVaultPath(e.target.value)}
                  placeholder="/path/to/your/obsidian/vault"
                  className="flex-1 rounded-[var(--radius-btn)] border border-[var(--border-subtle)] bg-[var(--bg-card)] px-3 py-2 text-sm text-[var(--text-primary)] placeholder:text-[var(--text-tertiary)] focus:outline-none focus:ring-2 focus:ring-[var(--brand-solid)]/40"
                />
                {isDesktopApp && (
                  <Button variant="outline" size="sm" onClick={() => void browseObsidianVault()}>
                    <FolderOpen className="h-4 w-4" />
                    Browse
                  </Button>
                )}
              </div>
            </div>
            <div className="flex items-end">
              <Button variant="brand" disabled={obsSaving} onClick={() => void saveObsidianConfig()}>
                {obsSaving ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
                {obsSaving ? "Saving..." : "Save"}
              </Button>
            </div>
          </div>
        )}

        {obsError && (
          <div className="text-sm rounded-md border border-[var(--error)]/30 bg-[var(--error)]/10 px-3 py-2 text-[var(--error)] flex items-start gap-2">
            <AlertCircle className="h-4 w-4 mt-0.5 shrink-0" />
            <span>{obsError}</span>
          </div>
        )}
        {obsSuccess && (
          <div className="text-sm rounded-md border border-[var(--success)]/30 bg-[var(--success)]/10 px-3 py-2 text-[var(--success)] flex items-start gap-2">
            <CheckCircle2 className="h-4 w-4 mt-0.5 shrink-0" />
            <span>{obsSuccess}</span>
          </div>
        )}
      </div>

      {/* OpenClaw Bridge */}
      <div className="bg-[var(--bg-main)]/50 rounded-[var(--radius-card)] shadow-sm border border-[var(--border-subtle)] p-6 space-y-4">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <div className="flex items-center gap-2">
              <Clapperboard className="h-5 w-5 text-[var(--text-primary)]" />
              <h2 className="text-xl font-bold text-[var(--text-primary)]">OpenClaw Bridge</h2>
            </div>
            <p className="text-[var(--text-secondary)] mt-1">
              Ingest audio files from a shared drop folder for batch transcription via OpenClaw.
            </p>
          </div>
          <Button
            variant="outline"
            disabled={!ocDropDir || ocIngesting}
            onClick={() => void ingestDropFolder()}
          >
            {ocIngesting ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
            {ocIngesting ? "Ingesting..." : "Ingest Drop Folder"}
          </Button>
        </div>

        {ocLoading ? (
          <div className="flex items-center gap-3 text-[var(--text-secondary)]">
            <Loader2 className="h-4 w-4 animate-spin" />
            Loading OpenClaw config...
          </div>
        ) : (
          <div className="flex flex-col sm:flex-row gap-3">
            <div className="flex-1">
              <label htmlFor="oc-drop-dir" className="block text-sm font-medium text-[var(--text-secondary)] mb-1">
                Drop Folder Path
              </label>
              <div className="flex gap-2">
                <input
                  id="oc-drop-dir"
                  type="text"
                  value={ocDropDir}
                  onChange={(e) => setOcDropDir(e.target.value)}
                  placeholder="/path/to/openclaw/drop"
                  className="flex-1 rounded-[var(--radius-btn)] border border-[var(--border-subtle)] bg-[var(--bg-card)] px-3 py-2 text-sm text-[var(--text-primary)] placeholder:text-[var(--text-tertiary)] focus:outline-none focus:ring-2 focus:ring-[var(--brand-solid)]/40"
                />
                {isDesktopApp && (
                  <Button variant="outline" size="sm" onClick={() => void browseOpenClawDrop()}>
                    <FolderOpen className="h-4 w-4" />
                    Browse
                  </Button>
                )}
              </div>
            </div>
            <div className="flex items-end">
              <Button variant="brand" disabled={ocSaving} onClick={() => void saveOpenClawConfig()}>
                {ocSaving ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
                {ocSaving ? "Saving..." : "Save"}
              </Button>
            </div>
          </div>
        )}

        {ocError && (
          <div className="text-sm rounded-md border border-[var(--error)]/30 bg-[var(--error)]/10 px-3 py-2 text-[var(--error)] flex items-start gap-2">
            <AlertCircle className="h-4 w-4 mt-0.5 shrink-0" />
            <span>{ocError}</span>
          </div>
        )}
        {ocSuccess && (
          <div className="text-sm rounded-md border border-[var(--success)]/30 bg-[var(--success)]/10 px-3 py-2 text-[var(--success)] flex items-start gap-2">
            <CheckCircle2 className="h-4 w-4 mt-0.5 shrink-0" />
            <span>{ocSuccess}</span>
          </div>
        )}
      </div>
    </div>
  );
}
