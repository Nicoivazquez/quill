import { useEffect, useMemo, useState } from 'react';
import { FolderOpen, Shield, Settings2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';

interface VaultRow {
    id: number;
    name: string;
    path: string;
    is_active: boolean;
}

interface SetupStateResponse {
    completed: boolean;
    auth_mode: string;
    vaults: VaultRow[];
    active_vault?: VaultRow;
    obsidian_vault_dir?: string;
    openclaw_drop_dir?: string;
}

interface SetupWizardProps {
    onComplete: () => Promise<void> | void;
}

type VaultSetupMode = 'create' | 'existing';

function deriveVaultNameFromPath(selectedPath: string): string {
    const trimmed = selectedPath.trim();
    if (!trimmed) return 'Vault';
    const normalized = trimmed.replace(/[\\/]+$/, '');
    const segments = normalized.split(/[\\/]/).filter(Boolean);
    if (segments.length === 0) return 'Vault';
    return segments[segments.length - 1];
}

function getDesktopBridge() {
    if (typeof window === 'undefined') {
        return undefined;
    }
    return window.quillDesktop;
}

export function SetupWizard({ onComplete }: SetupWizardProps) {
    const [loadingState, setLoadingState] = useState(true);
    const [submitting, setSubmitting] = useState(false);
    const [error, setError] = useState<string | null>(null);

    const [vaultPath, setVaultPath] = useState('');
    const [vaultName, setVaultName] = useState('Main Vault');
    const [vaultMode, setVaultMode] = useState<VaultSetupMode>('create');
    const [authMode, setAuthMode] = useState<'local' | 'server'>('local');
    const [obsidianVaultDir, setObsidianVaultDir] = useState('');
    const [openClawDropDir, setOpenClawDropDir] = useState('');
    const [showAdvanced, setShowAdvanced] = useState(false);
    const [vaults, setVaults] = useState<VaultRow[]>([]);
    const [pickingFolder, setPickingFolder] = useState<null | 'vault' | 'obsidian' | 'openclaw'>(null);

    const isDesktopApp = useMemo(() => {
        return typeof getDesktopBridge()?.selectFolder === 'function';
    }, []);

    useEffect(() => {
        const load = async () => {
            try {
                const response = await fetch('/api/v1/setup/state');
                if (!response.ok) {
                    throw new Error('Failed to load setup state');
                }

                const state = (await response.json()) as SetupStateResponse;
                setVaults(state.vaults || []);

                if (state.active_vault) {
                    setVaultPath(state.active_vault.path || '');
                    setVaultName(state.active_vault.name || 'Main Vault');
                    setVaultMode('existing');
                } else if (state.vaults && state.vaults.length > 0) {
                    const preferred = state.vaults.find((v) => v.is_active) || state.vaults[0];
                    setVaultPath(preferred.path || '');
                    setVaultName(preferred.name || 'Main Vault');
                    setVaultMode('existing');
                }

                if (typeof state.auth_mode === 'string' && state.auth_mode.toLowerCase() === 'server') {
                    setAuthMode('server');
                }

                if (state.obsidian_vault_dir) {
                    setObsidianVaultDir(state.obsidian_vault_dir);
                }
                if (state.openclaw_drop_dir) {
                    setOpenClawDropDir(state.openclaw_drop_dir);
                }
            } catch (e) {
                const message = e instanceof Error ? e.message : 'Failed to load setup state';
                setError(message);
            } finally {
                setLoadingState(false);
            }
        };

        load();
    }, []);

    const recommendedPath = useMemo(() => {
        if (vaultPath.trim()) return vaultPath;
        if (vaultMode === 'existing') return '/path/to/existing/vault';
        return '~/QuillVault';
    }, [vaultPath, vaultMode]);

    useEffect(() => {
        if (vaultMode !== 'existing') return;
        const derived = deriveVaultNameFromPath(vaultPath);
        if (derived) {
            setVaultName(derived);
        }
    }, [vaultMode, vaultPath]);

    const pickFolder = async (
        target: 'vault' | 'obsidian' | 'openclaw',
        options: { title: string; defaultPath?: string },
    ) => {
        const desktopBridge = getDesktopBridge();
        if (!desktopBridge?.selectFolder) {
            setError('Folder picker is available in the desktop app.');
            return;
        }

        setError(null);
        setPickingFolder(target);
        try {
            const selectedPath = await desktopBridge.selectFolder({
                title: options.title,
                defaultPath: options.defaultPath,
            });
            if (!selectedPath) {
                return;
            }

            if (target === 'vault') {
                setVaultPath(selectedPath);
                if (vaultMode === 'existing') {
                    setVaultName(deriveVaultNameFromPath(selectedPath));
                }
            } else if (target === 'obsidian') {
                setObsidianVaultDir(selectedPath);
            } else {
                setOpenClawDropDir(selectedPath);
            }
        } catch {
            setError('Unable to open folder picker.');
        } finally {
            setPickingFolder(null);
        }
    };

    const handleSubmit = async (event: React.FormEvent) => {
        event.preventDefault();
        setError(null);

        const trimmedPath = vaultPath.trim();
        if (!trimmedPath) {
            setError(vaultMode === 'existing' ? 'Existing vault path is required.' : 'Vault path is required.');
            return;
        }

        setSubmitting(true);
        try {
            const response = await fetch('/api/v1/setup/complete', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({
                    vault_path: trimmedPath,
                    vault_mode: vaultMode,
                    auth_mode: authMode,
                    obsidian_vault_dir: obsidianVaultDir.trim() || undefined,
                    openclaw_drop_dir: openClawDropDir.trim() || undefined,
                    ...(vaultMode === 'create'
                        ? { vault_name: vaultName.trim() || 'Main Vault' }
                        : {}),
                }),
            });

            if (!response.ok) {
                const data = await response.json().catch(() => null);
                throw new Error(data?.error || 'Failed to complete setup');
            }

            await onComplete();
        } catch (e) {
            const message = e instanceof Error ? e.message : 'Failed to complete setup';
            setError(message);
        } finally {
            setSubmitting(false);
        }
    };

    if (loadingState) {
        return (
            <div className="min-h-screen flex items-center justify-center bg-[var(--bg-main)]">
                <div className="h-8 w-8 animate-spin rounded-full border-b-2 border-[var(--brand-solid)]" />
            </div>
        );
    }

    return (
        <div className="min-h-screen bg-[var(--bg-main)] px-4 py-10 sm:px-6">
            <div className="mx-auto w-full max-w-3xl rounded-[var(--radius-card)] border border-[var(--border-subtle)] bg-[var(--bg-card)] p-6 shadow-[var(--shadow-float)] sm:p-8">
                <div className="mb-8">
                    <p className="text-xs uppercase tracking-[0.2em] text-[var(--text-tertiary)]">Quill Local Setup</p>
                    <h1 className="mt-3 text-3xl font-bold tracking-tight text-[var(--text-primary)]">Choose Your Vault</h1>
                    <p className="mt-2 text-sm text-[var(--text-secondary)]">
                        Quill runs local-first. Pick the folder that will hold media, transcript artifacts, and contacts.
                    </p>
                </div>

                <form className="space-y-6" onSubmit={handleSubmit}>
                    <div className="space-y-2">
                        <label className="text-sm font-medium text-[var(--text-primary)]">Vault Setup</label>
                        <div className="grid gap-3 sm:grid-cols-2">
                            <button
                                type="button"
                                className={`rounded-[var(--radius-btn)] border px-3 py-3 text-left transition ${vaultMode === 'create'
                                    ? 'border-[var(--brand-solid)] bg-[var(--brand-light)]/40'
                                    : 'border-[var(--border-subtle)] hover:border-[var(--brand-solid)]/40'
                                    }`}
                                onClick={() => setVaultMode('create')}
                            >
                                <p className="text-sm font-medium text-[var(--text-primary)]">Create new vault</p>
                                <p className="mt-1 text-xs text-[var(--text-tertiary)]">
                                    Use a new folder. Quill creates the vault structure automatically.
                                </p>
                            </button>
                            <button
                                type="button"
                                className={`rounded-[var(--radius-btn)] border px-3 py-3 text-left transition ${vaultMode === 'existing'
                                    ? 'border-[var(--brand-solid)] bg-[var(--brand-light)]/40'
                                    : 'border-[var(--border-subtle)] hover:border-[var(--brand-solid)]/40'
                                    }`}
                                onClick={() => setVaultMode('existing')}
                            >
                                <p className="text-sm font-medium text-[var(--text-primary)]">Use existing vault</p>
                                <p className="mt-1 text-xs text-[var(--text-tertiary)]">
                                    Reconnect to a folder that already has Quill vault data.
                                </p>
                            </button>
                        </div>
                    </div>

                    {vaultMode === 'create' && (
                        <div className="space-y-2">
                            <label className="text-sm font-medium text-[var(--text-primary)]">Vault Name</label>
                            <Input
                                value={vaultName}
                                onChange={(event) => setVaultName(event.target.value)}
                                placeholder="Main Vault"
                            />
                        </div>
                    )}

                    <div className="space-y-2">
                        <label className="text-sm font-medium text-[var(--text-primary)]">
                            {vaultMode === 'existing' ? 'Existing Vault Path' : 'New Vault Path'}
                        </label>
                        <div className="flex items-center gap-2">
                            <FolderOpen className="h-4 w-4 text-[var(--text-tertiary)]" />
                            <Input
                                value={vaultPath}
                                onChange={(event) => setVaultPath(event.target.value)}
                                placeholder={recommendedPath}
                            />
                            <Button
                                type="button"
                                variant="outline"
                                disabled={!isDesktopApp || pickingFolder === 'vault'}
                                onClick={() => pickFolder('vault', {
                                    title: vaultMode === 'existing' ? 'Select Existing Vault Folder' : 'Select Vault Folder',
                                    defaultPath: vaultPath || undefined,
                                })}
                            >
                                {pickingFolder === 'vault' ? 'Opening...' : 'Browse'}
                            </Button>
                        </div>
                        {vaultMode === 'existing' ? (
                            <p className="text-xs text-[var(--text-tertiary)]">
                                Select a folder from a previous Quill install (`.quill`, `Inbox`, `Media`, or `Transcripts`).
                            </p>
                        ) : (
                            <p className="text-xs text-[var(--text-tertiary)]">
                                Subfolders created automatically: `Inbox`, `Media`, `Transcripts`, `.quill`, `Contacts/Snippets`.
                            </p>
                        )}
                        {!isDesktopApp && (
                            <p className="text-xs text-[var(--text-tertiary)]">
                                Folder picker appears in the desktop app.
                            </p>
                        )}
                    </div>

                    {vaults.length > 0 && (
                        <div className="rounded-[var(--radius-btn)] border border-[var(--border-subtle)] bg-[var(--secondary)]/20 p-3">
                            <p className="mb-2 text-xs font-semibold uppercase tracking-wider text-[var(--text-tertiary)]">Existing Vaults</p>
                            <div className="space-y-1 text-sm">
                                {vaults.map((vault) => (
                                    <button
                                        key={vault.id}
                                        type="button"
                                        className="block w-full rounded px-2 py-1 text-left hover:bg-[var(--brand-light)]"
                                        onClick={() => {
                                            setVaultMode('existing');
                                            setVaultPath(vault.path);
                                            setVaultName(vault.name || deriveVaultNameFromPath(vault.path));
                                        }}
                                    >
                                        <span className="font-medium text-[var(--text-primary)]">{vault.name}</span>
                                        <span className="ml-2 text-xs text-[var(--text-tertiary)]">{vault.path}</span>
                                        {vault.is_active && <span className="ml-2 text-xs text-[var(--brand-solid)]">active</span>}
                                    </button>
                                ))}
                            </div>
                        </div>
                    )}

                    <div className="space-y-3 rounded-[var(--radius-btn)] border border-[var(--border-subtle)] p-4">
                        <button
                            type="button"
                            className="flex w-full items-center justify-between text-left"
                            onClick={() => setShowAdvanced((current) => !current)}
                        >
                            <span className="flex items-center gap-2 text-sm font-medium text-[var(--text-primary)]">
                                <Settings2 className="h-4 w-4" />
                                Advanced Setup
                            </span>
                            <span className="text-xs text-[var(--text-tertiary)]">{showAdvanced ? 'Hide' : 'Show'}</span>
                        </button>

                        {showAdvanced && (
                            <div className="space-y-4">
                                <div>
                                    <label className="mb-2 flex items-center gap-2 text-sm font-medium text-[var(--text-primary)]">
                                        <Shield className="h-4 w-4" /> Auth Mode
                                    </label>
                                    <div className="flex gap-2">
                                        <Button
                                            type="button"
                                            variant={authMode === 'local' ? 'default' : 'outline'}
                                            onClick={() => setAuthMode('local')}
                                        >
                                            Local (No Login)
                                        </Button>
                                        <Button
                                            type="button"
                                            variant={authMode === 'server' ? 'default' : 'outline'}
                                            onClick={() => setAuthMode('server')}
                                        >
                                            Server (Login)
                                        </Button>
                                    </div>
                                </div>

                                <div className="space-y-2">
                                    <label className="text-sm font-medium text-[var(--text-primary)]">Obsidian Vault Path (optional)</label>
                                    <div className="flex items-center gap-2">
                                        <Input
                                            value={obsidianVaultDir}
                                            onChange={(event) => setObsidianVaultDir(event.target.value)}
                                            placeholder="/Users/you/Documents/ObsidianVault"
                                        />
                                        <Button
                                            type="button"
                                            variant="outline"
                                            disabled={!isDesktopApp || pickingFolder === 'obsidian'}
                                            onClick={() => pickFolder('obsidian', {
                                                title: 'Select Obsidian Vault Folder',
                                                defaultPath: obsidianVaultDir || undefined,
                                            })}
                                        >
                                            {pickingFolder === 'obsidian' ? 'Opening...' : 'Browse'}
                                        </Button>
                                    </div>
                                </div>

                                <div className="space-y-2">
                                    <label className="text-sm font-medium text-[var(--text-primary)]">OpenClaw Drop Folder (optional)</label>
                                    <div className="flex items-center gap-2">
                                        <Input
                                            value={openClawDropDir}
                                            onChange={(event) => setOpenClawDropDir(event.target.value)}
                                            placeholder="/Users/you/OpenClawDrops"
                                        />
                                        <Button
                                            type="button"
                                            variant="outline"
                                            disabled={!isDesktopApp || pickingFolder === 'openclaw'}
                                            onClick={() => pickFolder('openclaw', {
                                                title: 'Select OpenClaw Drop Folder',
                                                defaultPath: openClawDropDir || undefined,
                                            })}
                                        >
                                            {pickingFolder === 'openclaw' ? 'Opening...' : 'Browse'}
                                        </Button>
                                    </div>
                                </div>
                            </div>
                        )}
                    </div>

                    {error && (
                        <div className="rounded-[var(--radius-btn)] border border-red-400/40 bg-red-500/10 px-3 py-2 text-sm text-red-500">
                            {error}
                        </div>
                    )}

                    <Button type="submit" className="w-full" disabled={submitting}>
                        {submitting ? 'Saving Setup...' : 'Start With This Vault'}
                    </Button>
                </form>
            </div>
        </div>
    );
}
