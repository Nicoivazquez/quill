import { useState, useCallback, useRef, useEffect } from "react";
import {
    Folder,
    FolderOpen,
    FolderPlus,
    ChevronRight,
    ChevronDown,
    Pencil,
    Trash2,
    FileAudio,
    MoreHorizontal,
    FolderInput,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { sanitizeInputValue } from "@/lib/filename-validation";
import { Button } from "@/components/ui/button";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
    AlertDialog,
    AlertDialogAction,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import {
    useFolders,
    useCreateFolder,
    useRenameFolder,
    useDeleteFolder,
    useMoveFolder,
    useMoveToFolder,
} from "@/features/transcription/hooks/useFolders";

interface FolderSidebarProps {
    selectedFolder: string | null; // null = "All", "" = root only
    onFolderSelect: (folder: string | null) => void;
}

interface FolderNode {
    name: string;
    path: string;
    children: FolderNode[];
}

function buildFolderTree(folders: string[]): FolderNode[] {
    const root: FolderNode[] = [];

    for (const folderPath of folders) {
        const parts = folderPath.split("/");
        let current = root;

        for (let i = 0; i < parts.length; i++) {
            const part = parts[i];
            const path = parts.slice(0, i + 1).join("/");
            let existing = current.find((n) => n.name === part);

            if (!existing) {
                existing = { name: part, path, children: [] };
                current.push(existing);
            }

            current = existing.children;
        }
    }

    return root;
}

// Drag data type constants
export const DND_TYPE_FILES = "application/x-quill-files";
export const DND_TYPE_FOLDER = "application/x-quill-folder";

function FolderTreeItem({
    node,
    level,
    selectedFolder,
    onSelect,
    onRename,
    onDelete,
    onCreateSubfolder,
    onDropFiles,
    onDropFolder,
    creatingSubfolderIn,
    renderSubfolderInput,
}: {
    node: FolderNode;
    level: number;
    selectedFolder: string | null;
    onSelect: (folder: string) => void;
    onRename: (oldName: string) => void;
    onDelete: (name: string) => void;
    onCreateSubfolder: (parentPath: string) => void;
    onDropFiles: (jobIds: string[], targetFolder: string) => void;
    onDropFolder: (srcFolder: string, destParent: string) => void;
    creatingSubfolderIn: string | null;
    renderSubfolderInput: (parentPath: string, level: number) => React.ReactNode;
}) {
    const [expanded, setExpanded] = useState(true);
    const [dragOver, setDragOver] = useState(false);
    const hasChildren = node.children.length > 0;
    const isSelected = selectedFolder === node.path;

    const handleDragOver = useCallback((e: React.DragEvent) => {
        if (e.dataTransfer.types.includes(DND_TYPE_FILES) || e.dataTransfer.types.includes(DND_TYPE_FOLDER)) {
            e.preventDefault();
            e.dataTransfer.dropEffect = "move";
            setDragOver(true);
        }
    }, []);

    const handleDragLeave = useCallback((e: React.DragEvent) => {
        // Only clear if leaving the folder item itself (not entering a child)
        if (!e.currentTarget.contains(e.relatedTarget as Node)) {
            setDragOver(false);
        }
    }, []);

    const handleDrop = useCallback((e: React.DragEvent) => {
        e.preventDefault();
        setDragOver(false);

        const filesData = e.dataTransfer.getData(DND_TYPE_FILES);
        if (filesData) {
            try {
                const jobIds = JSON.parse(filesData) as string[];
                onDropFiles(jobIds, node.path);
            } catch { /* ignore parse errors */ }
            return;
        }

        const folderData = e.dataTransfer.getData(DND_TYPE_FOLDER);
        if (folderData) {
            // Don't allow dropping a folder onto itself
            if (folderData !== node.path && !node.path.startsWith(folderData + "/")) {
                onDropFolder(folderData, node.path);
            }
        }
    }, [node.path, onDropFiles, onDropFolder]);

    const handleDragStart = useCallback((e: React.DragEvent) => {
        e.dataTransfer.setData(DND_TYPE_FOLDER, node.path);
        e.dataTransfer.effectAllowed = "move";
    }, [node.path]);

    return (
        <div>
            <div
                draggable
                onDragStart={handleDragStart}
                onDragOver={handleDragOver}
                onDragLeave={handleDragLeave}
                onDrop={handleDrop}
                className={cn(
                    "group flex items-center gap-1 py-1.5 px-2 rounded-[var(--radius-btn)] cursor-pointer transition-colors text-sm",
                    "hover:bg-[var(--bg-elevated)]",
                    isSelected &&
                        "bg-[var(--brand-light)] dark:bg-[var(--accent)] text-[var(--brand-solid)] font-medium",
                    dragOver &&
                        "ring-2 ring-[var(--brand-solid)] bg-[var(--brand-light)] dark:bg-[var(--accent)]"
                )}
                style={{ paddingLeft: `${level * 16 + 8}px` }}
                onClick={() => onSelect(node.path)}
            >
                {hasChildren ? (
                    <button
                        className="p-0.5 hover:bg-[var(--bg-card)] rounded transition-colors"
                        onClick={(e) => {
                            e.stopPropagation();
                            setExpanded(!expanded);
                        }}
                    >
                        {expanded ? (
                            <ChevronDown className="h-3.5 w-3.5 text-[var(--text-tertiary)]" />
                        ) : (
                            <ChevronRight className="h-3.5 w-3.5 text-[var(--text-tertiary)]" />
                        )}
                    </button>
                ) : (
                    <span className="w-5" />
                )}

                {isSelected || expanded ? (
                    <FolderOpen className="h-4 w-4 flex-shrink-0 text-[var(--brand-solid)]" />
                ) : (
                    <Folder className="h-4 w-4 flex-shrink-0 text-[var(--text-tertiary)]" />
                )}

                <span className="truncate flex-1">{node.name}</span>

                <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                        <button
                            className="p-1 rounded opacity-0 group-hover:opacity-100 hover:bg-[var(--bg-card)] transition-all"
                            onClick={(e) => e.stopPropagation()}
                        >
                            <MoreHorizontal className="h-3.5 w-3.5 text-[var(--text-tertiary)]" />
                        </button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end" className="w-44">
                        <DropdownMenuItem onClick={() => onCreateSubfolder(node.path)}>
                            <FolderPlus className="h-3.5 w-3.5 mr-2" />
                            New Subfolder
                        </DropdownMenuItem>
                        <DropdownMenuItem onClick={() => onRename(node.path)}>
                            <Pencil className="h-3.5 w-3.5 mr-2" />
                            Rename
                        </DropdownMenuItem>
                        <DropdownMenuItem
                            onClick={() => onDelete(node.path)}
                            className="text-[var(--error)]"
                        >
                            <Trash2 className="h-3.5 w-3.5 mr-2" />
                            Delete
                        </DropdownMenuItem>
                    </DropdownMenuContent>
                </DropdownMenu>
            </div>

            {(hasChildren || creatingSubfolderIn === node.path) && expanded && (
                <div>
                    {node.children.map((child) => (
                        <FolderTreeItem
                            key={child.path}
                            node={child}
                            level={level + 1}
                            selectedFolder={selectedFolder}
                            onSelect={onSelect}
                            onRename={onRename}
                            onDelete={onDelete}
                            onCreateSubfolder={onCreateSubfolder}
                            onDropFiles={onDropFiles}
                            onDropFolder={onDropFolder}
                            creatingSubfolderIn={creatingSubfolderIn}
                            renderSubfolderInput={renderSubfolderInput}
                        />
                    ))}
                    {renderSubfolderInput(node.path, level)}
                </div>
            )}
        </div>
    );
}

export function FolderSidebar({ selectedFolder, onFolderSelect }: FolderSidebarProps) {
    const { data: folders = [], isLoading } = useFolders();
    const createFolder = useCreateFolder();
    const renameFolder = useRenameFolder();
    const deleteFolder = useDeleteFolder();
    const moveFolder = useMoveFolder();
    const moveToFolder = useMoveToFolder();

    const [isCreating, setIsCreating] = useState(false);
    const [newFolderName, setNewFolderName] = useState("");
    const [renamingFolder, setRenamingFolder] = useState<string | null>(null);
    const [renameValue, setRenameValue] = useState("");
    const [deletingFolder, setDeletingFolder] = useState<string | null>(null);
    const [creatingSubfolderIn, setCreatingSubfolderIn] = useState<string | null>(null);
    const [subfolderName, setSubfolderName] = useState("");
    const [rootDragOver, setRootDragOver] = useState(false);

    const createInputRef = useRef<HTMLInputElement>(null);
    const renameInputRef = useRef<HTMLInputElement>(null);
    const subfolderInputRef = useRef<HTMLInputElement>(null);

    useEffect(() => {
        if (isCreating && createInputRef.current) {
            createInputRef.current.focus();
        }
    }, [isCreating]);

    useEffect(() => {
        if (renamingFolder && renameInputRef.current) {
            renameInputRef.current.focus();
            renameInputRef.current.select();
        }
    }, [renamingFolder]);

    useEffect(() => {
        if (creatingSubfolderIn && subfolderInputRef.current) {
            subfolderInputRef.current.focus();
        }
    }, [creatingSubfolderIn]);

    const tree = buildFolderTree(folders);

    const handleCreate = useCallback(async () => {
        const name = newFolderName.trim();
        if (!name) {
            setIsCreating(false);
            return;
        }
        try {
            await createFolder.mutateAsync(name);
            setNewFolderName("");
            setIsCreating(false);
        } catch {
            // Error handled by mutation
        }
    }, [newFolderName, createFolder]);

    const handleCreateSubfolder = useCallback(async () => {
        const name = subfolderName.trim();
        if (!name || !creatingSubfolderIn) {
            setCreatingSubfolderIn(null);
            setSubfolderName("");
            return;
        }
        const fullPath = `${creatingSubfolderIn}/${name}`;
        try {
            await createFolder.mutateAsync(fullPath);
            setSubfolderName("");
            setCreatingSubfolderIn(null);
        } catch {
            // Error handled by mutation
        }
    }, [subfolderName, creatingSubfolderIn, createFolder]);

    const handleRename = useCallback(async () => {
        const newSegment = renameValue.trim();
        if (!newSegment || !renamingFolder) {
            setRenamingFolder(null);
            return;
        }
        // Preserve parent path for nested folders
        const parts = renamingFolder.split("/");
        parts[parts.length - 1] = newSegment;
        const newName = parts.join("/");
        if (newName === renamingFolder) {
            setRenamingFolder(null);
            return;
        }
        try {
            await renameFolder.mutateAsync({ oldName: renamingFolder, newName });
            if (selectedFolder === renamingFolder) {
                onFolderSelect(newName);
            }
            setRenamingFolder(null);
        } catch {
            // Error handled by mutation
        }
    }, [renameValue, renamingFolder, renameFolder, selectedFolder, onFolderSelect]);

    const handleDelete = useCallback(async () => {
        if (!deletingFolder) return;
        try {
            await deleteFolder.mutateAsync(deletingFolder);
            if (selectedFolder === deletingFolder) {
                onFolderSelect(null);
            }
            setDeletingFolder(null);
        } catch {
            // Error handled by mutation
        }
    }, [deletingFolder, deleteFolder, selectedFolder, onFolderSelect]);

    const startRename = useCallback((folderPath: string) => {
        setRenamingFolder(folderPath);
        // Use just the last segment for the rename input
        const parts = folderPath.split("/");
        setRenameValue(parts[parts.length - 1]);
    }, []);

    const startCreateSubfolder = useCallback((parentPath: string) => {
        setCreatingSubfolderIn(parentPath);
        setSubfolderName("");
    }, []);

    const handleDropFiles = useCallback(async (jobIds: string[], targetFolder: string) => {
        for (const jobId of jobIds) {
            try {
                await moveToFolder.mutateAsync({ jobId, folder: targetFolder });
            } catch {
                // Error handled by mutation
            }
        }
    }, [moveToFolder]);

    const handleDropFolder = useCallback(async (srcFolder: string, destParent: string) => {
        try {
            await moveFolder.mutateAsync({ folder: srcFolder, destParent });
        } catch {
            // Error handled by mutation
        }
    }, [moveFolder]);

    // Handle drop on "Unfiled" (root) — move files out of folders
    const handleRootDragOver = useCallback((e: React.DragEvent) => {
        if (e.dataTransfer.types.includes(DND_TYPE_FILES) || e.dataTransfer.types.includes(DND_TYPE_FOLDER)) {
            e.preventDefault();
            e.dataTransfer.dropEffect = "move";
            setRootDragOver(true);
        }
    }, []);

    const handleRootDragLeave = useCallback((e: React.DragEvent) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node)) {
            setRootDragOver(false);
        }
    }, []);

    const handleRootDrop = useCallback((e: React.DragEvent) => {
        e.preventDefault();
        setRootDragOver(false);

        const filesData = e.dataTransfer.getData(DND_TYPE_FILES);
        if (filesData) {
            try {
                const jobIds = JSON.parse(filesData) as string[];
                handleDropFiles(jobIds, "");
            } catch { /* ignore */ }
            return;
        }

        const folderData = e.dataTransfer.getData(DND_TYPE_FOLDER);
        if (folderData) {
            handleDropFolder(folderData, "");
        }
    }, [handleDropFiles, handleDropFolder]);

    // Render the subfolder input inline beneath the target folder's children
    const renderSubfolderInput = useCallback((parentPath: string, level: number) => {
        if (creatingSubfolderIn !== parentPath) return null;
        return (
            <div className="px-2 py-1" style={{ paddingLeft: `${(level + 1) * 16 + 8}px` }}>
                <div className="flex items-center gap-1.5">
                    <FolderPlus className="h-3.5 w-3.5 text-[var(--brand-solid)] flex-shrink-0" />
                    <input
                        ref={subfolderInputRef}
                        value={subfolderName}
                        onChange={(e) => setSubfolderName(sanitizeInputValue(e.target.value))}
                        onBlur={handleCreateSubfolder}
                        onKeyDown={(e) => {
                            if (e.key === "Enter") handleCreateSubfolder();
                            if (e.key === "Escape") {
                                setCreatingSubfolderIn(null);
                                setSubfolderName("");
                            }
                        }}
                        placeholder="Subfolder name..."
                        className="flex-1 px-2 py-0.5 text-sm rounded border border-[var(--brand-solid)] bg-[var(--bg-elevated)] text-[var(--text-primary)] outline-none placeholder:text-[var(--text-tertiary)]"
                    />
                </div>
            </div>
        );
    }, [creatingSubfolderIn, subfolderName, handleCreateSubfolder]);

    return (
        <div className="flex flex-col h-full">
            {/* Header */}
            <div className="flex items-center justify-between px-3 py-2.5 border-b border-[var(--border-subtle)]">
                <span className="text-xs font-semibold uppercase tracking-wider text-[var(--text-tertiary)]">
                    Folders
                </span>
                <Button
                    variant="ghost"
                    size="icon"
                    className="h-6 w-6 rounded"
                    onClick={() => setIsCreating(true)}
                >
                    <FolderPlus className="h-3.5 w-3.5" />
                </Button>
            </div>

            {/* Folder Tree */}
            <div className="flex-1 overflow-y-auto py-2 px-1 custom-scrollbar">
                {/* All Files */}
                <div
                    className={cn(
                        "flex items-center gap-2 py-1.5 px-2 rounded-[var(--radius-btn)] cursor-pointer transition-colors text-sm",
                        "hover:bg-[var(--bg-elevated)]",
                        selectedFolder === null &&
                            "bg-[var(--brand-light)] dark:bg-[var(--accent)] text-[var(--brand-solid)] font-medium"
                    )}
                    onClick={() => onFolderSelect(null)}
                >
                    <FileAudio className="h-4 w-4 flex-shrink-0" />
                    <span>All Files</span>
                </div>

                {/* Root (unfiled) — also a drop target */}
                <div
                    className={cn(
                        "flex items-center gap-2 py-1.5 px-2 rounded-[var(--radius-btn)] cursor-pointer transition-colors text-sm",
                        "hover:bg-[var(--bg-elevated)]",
                        selectedFolder === "" &&
                            "bg-[var(--brand-light)] dark:bg-[var(--accent)] text-[var(--brand-solid)] font-medium",
                        rootDragOver &&
                            "ring-2 ring-[var(--brand-solid)] bg-[var(--brand-light)] dark:bg-[var(--accent)]"
                    )}
                    onClick={() => onFolderSelect("")}
                    onDragOver={handleRootDragOver}
                    onDragLeave={handleRootDragLeave}
                    onDrop={handleRootDrop}
                >
                    <FolderInput className="h-4 w-4 flex-shrink-0 text-[var(--text-tertiary)]" />
                    <span>Unfiled</span>
                </div>

                {/* Separator */}
                {folders.length > 0 && (
                    <div className="h-px bg-[var(--border-subtle)] mx-2 my-1.5" />
                )}

                {isLoading ? (
                    <div className="space-y-2 px-2 pt-1">
                        {Array.from({ length: 3 }).map((_, i) => (
                            <div
                                key={i}
                                className="h-7 bg-[var(--bg-elevated)] rounded animate-pulse"
                            />
                        ))}
                    </div>
                ) : (
                    tree.map((node) =>
                        renamingFolder === node.path ? (
                            <div key={node.path} className="px-2 py-1">
                                <input
                                    ref={renameInputRef}
                                    value={renameValue}
                                    onChange={(e) => setRenameValue(sanitizeInputValue(e.target.value))}
                                    onBlur={handleRename}
                                    onKeyDown={(e) => {
                                        if (e.key === "Enter") handleRename();
                                        if (e.key === "Escape") setRenamingFolder(null);
                                    }}
                                    className="w-full px-2 py-1 text-sm rounded border border-[var(--brand-solid)] bg-[var(--bg-elevated)] text-[var(--text-primary)] outline-none"
                                />
                            </div>
                        ) : (
                            <div key={node.path}>
                                <FolderTreeItem
                                    node={node}
                                    level={0}
                                    selectedFolder={selectedFolder}
                                    onSelect={onFolderSelect}
                                    onRename={startRename}
                                    onDelete={setDeletingFolder}
                                    onCreateSubfolder={startCreateSubfolder}
                                    onDropFiles={handleDropFiles}
                                    onDropFolder={handleDropFolder}
                                    creatingSubfolderIn={creatingSubfolderIn}
                                    renderSubfolderInput={renderSubfolderInput}
                                />
                                {renderSubfolderInput(node.path, 0)}
                            </div>
                        )
                    )
                )}

                {/* New Folder Input */}
                {isCreating && (
                    <div className="px-2 py-1">
                        <div className="flex items-center gap-1.5">
                            <FolderPlus className="h-4 w-4 text-[var(--brand-solid)] flex-shrink-0" />
                            <input
                                ref={createInputRef}
                                value={newFolderName}
                                onChange={(e) => setNewFolderName(sanitizeInputValue(e.target.value))}
                                onBlur={handleCreate}
                                onKeyDown={(e) => {
                                    if (e.key === "Enter") handleCreate();
                                    if (e.key === "Escape") {
                                        setIsCreating(false);
                                        setNewFolderName("");
                                    }
                                }}
                                placeholder="Folder name..."
                                className="flex-1 px-2 py-1 text-sm rounded border border-[var(--brand-solid)] bg-[var(--bg-elevated)] text-[var(--text-primary)] outline-none placeholder:text-[var(--text-tertiary)]"
                            />
                        </div>
                    </div>
                )}
            </div>

            {/* Delete Confirmation Dialog */}
            <AlertDialog
                open={!!deletingFolder}
                onOpenChange={(open) => {
                    if (!open) setDeletingFolder(null);
                }}
            >
                <AlertDialogContent className="glass-card bg-[var(--bg-main)]/90 border-[var(--border-subtle)]">
                    <AlertDialogHeader>
                        <AlertDialogTitle className="text-[var(--text-primary)]">
                            Delete Folder
                        </AlertDialogTitle>
                        <AlertDialogDescription className="text-[var(--text-secondary)]">
                            Are you sure you want to delete "{deletingFolder}"? The folder must be
                            empty. Any transcripts inside must be moved first.
                        </AlertDialogDescription>
                    </AlertDialogHeader>
                    <AlertDialogFooter>
                        <AlertDialogCancel className="bg-[var(--secondary)] border-[var(--border-subtle)] text-[var(--text-secondary)] hover:bg-[var(--bg-card)]">
                            Cancel
                        </AlertDialogCancel>
                        <AlertDialogAction
                            className="bg-[var(--error)] text-white hover:opacity-90"
                            onClick={handleDelete}
                        >
                            Delete
                        </AlertDialogAction>
                    </AlertDialogFooter>
                </AlertDialogContent>
            </AlertDialog>
        </div>
    );
}
