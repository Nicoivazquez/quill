import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Folder, FolderOpen, HardDrive, Plus } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useAuth } from '@/features/auth/hooks/useAuth';

interface Vault {
    id: number;
    name: string;
    path: string;
    is_active: boolean;
}

interface SetupState {
    active_vault?: Vault;
    vaults: Vault[];
}

interface VaultSidebarProps {
    compact?: boolean;
}

export function VaultSidebar({ compact = false }: VaultSidebarProps) {
    const queryClient = useQueryClient();
    const { getAuthHeaders } = useAuth();
    const [newVaultName, setNewVaultName] = useState('');
    const [newVaultPath, setNewVaultPath] = useState('');
    const [showCreateForm, setShowCreateForm] = useState(false);

    const { data, isLoading } = useQuery({
        queryKey: ['vaultSetupState'],
        queryFn: async () => {
            const response = await fetch('/api/v1/setup/state', {
                headers: getAuthHeaders(),
            });
            if (!response.ok) {
                throw new Error('Failed to load vaults');
            }
            return response.json() as Promise<SetupState>;
        },
    });

    const activeVault = useMemo(
        () => data?.active_vault || data?.vaults?.find((vault) => vault.is_active),
        [data],
    );

    const activateVault = useMutation({
        mutationFn: async (vaultID: number) => {
            const response = await fetch(`/api/v1/vaults/${vaultID}/activate`, {
                method: 'POST',
                headers: getAuthHeaders(),
            });
            if (!response.ok) {
                const payload = await response.json().catch(() => null);
                throw new Error(payload?.error || 'Failed to activate vault');
            }
            return response.json();
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['vaultSetupState'] });
            queryClient.invalidateQueries({ queryKey: ['audioFiles'] });
        },
    });

    const createVault = useMutation({
        mutationFn: async () => {
            const response = await fetch('/api/v1/vaults', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    ...getAuthHeaders(),
                },
                body: JSON.stringify({
                    name: newVaultName.trim() || 'Vault',
                    path: newVaultPath.trim(),
                }),
            });
            if (!response.ok) {
                const payload = await response.json().catch(() => null);
                throw new Error(payload?.error || 'Failed to create vault');
            }
            return response.json();
        },
        onSuccess: () => {
            setNewVaultName('');
            setNewVaultPath('');
            setShowCreateForm(false);
            queryClient.invalidateQueries({ queryKey: ['vaultSetupState'] });
        },
    });

    return (
        <aside className={`obsidian-pane p-3 ${compact ? '' : 'h-fit lg:sticky lg:top-[88px]'}`}>
            <div className="mb-4 flex items-center justify-between">
                <div>
                    <p className="text-xs uppercase tracking-wider text-[var(--text-tertiary)]">Vault</p>
                    <h2 className="text-sm font-semibold text-[var(--text-primary)]">Local Library</h2>
                </div>
                <Button
                    type="button"
                    size="icon"
                    variant="ghost"
                    className="h-8 w-8 border border-transparent hover:border-[var(--border-subtle)] hover:bg-[var(--bg-muted-pane)]"
                    onClick={() => setShowCreateForm((current) => !current)}
                >
                    <Plus className="h-4 w-4" />
                </Button>
            </div>

            {activeVault && (
                <div className="mb-4 rounded-[var(--radius-btn)] bg-[var(--bg-muted-pane)] border border-[var(--border-subtle)] p-3 text-xs">
                    <div className="flex items-center gap-2 text-[var(--brand-solid)]">
                        <HardDrive className="h-3.5 w-3.5" />
                        <span className="font-semibold">Active</span>
                    </div>
                    <p className="mt-1 text-sm font-medium text-[var(--text-primary)]">{activeVault.name}</p>
                    <p className="mt-1 break-all text-[var(--text-tertiary)]">{activeVault.path}</p>
                </div>
            )}

            {showCreateForm && (
                <div className="mb-4 space-y-2 rounded-[var(--radius-btn)] border border-[var(--border-subtle)] bg-[var(--bg-muted-pane)] p-3">
                    <Input
                        value={newVaultName}
                        onChange={(event) => setNewVaultName(event.target.value)}
                        placeholder="Vault name"
                    />
                    <Input
                        value={newVaultPath}
                        onChange={(event) => setNewVaultPath(event.target.value)}
                        placeholder="/path/to/vault"
                    />
                    <Button
                        type="button"
                        className="w-full rounded-[var(--radius-btn)]"
                        disabled={!newVaultPath.trim() || createVault.isPending}
                        onClick={() => createVault.mutate()}
                    >
                        {createVault.isPending ? 'Creating...' : 'Create Vault'}
                    </Button>
                    {createVault.error && (
                        <p className="text-xs text-red-500">{(createVault.error as Error).message}</p>
                    )}
                </div>
            )}

            <div className="space-y-1">
                {isLoading && <p className="text-xs text-[var(--text-tertiary)]">Loading vaults...</p>}
                {data?.vaults?.map((vault) => {
                    const isActive = !!vault.is_active;
                    return (
                        <button
                            key={vault.id}
                            type="button"
                            className={`w-full rounded-[var(--radius-btn)] px-3 py-2 text-left transition border ${isActive ? 'bg-[var(--bg-muted-pane)] border-[var(--brand-solid)]/30 text-[var(--brand-solid)]' : 'border-transparent hover:bg-[var(--bg-muted-pane)] hover:border-[var(--border-subtle)]'}`}
                            onClick={() => {
                                if (!isActive) {
                                    activateVault.mutate(vault.id);
                                }
                            }}
                        >
                            <div className="flex items-center gap-2">
                                {isActive ? <FolderOpen className="h-4 w-4" /> : <Folder className="h-4 w-4" />}
                                <span className="text-sm font-medium">{vault.name}</span>
                            </div>
                            <p className="mt-1 truncate text-xs text-[var(--text-tertiary)]">{vault.path}</p>
                        </button>
                    );
                })}
                {activateVault.error && (
                    <p className="text-xs text-red-500">{(activateVault.error as Error).message}</p>
                )}
            </div>

            <div className="mt-4 border-t border-[var(--border-subtle)] pt-3">
                <p className="text-xs uppercase tracking-wider text-[var(--text-tertiary)]">Structure</p>
                <p className="mt-1 text-xs text-[var(--text-secondary)]">Inbox / Media / Transcripts / .quill</p>
            </div>
        </aside>
    );
}
