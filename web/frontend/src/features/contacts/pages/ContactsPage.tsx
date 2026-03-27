import { useEffect, useMemo, useRef, useState } from "react";
import {
  ArrowLeft,
  FileAudio,
  FileJson,
  FolderTree,
  RefreshCcw,
  Save,
  Trash2,
  Upload,
  UserPlus,
  WandSparkles,
  X,
} from "lucide-react";
import { MainLayout } from "@/components/layout/MainLayout";
import { Header } from "@/components/Header";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  useContact,
  useContactFiles,
  useContacts,
  useCreateContact,
  useDeleteContact,
  useDeleteSignature,
  useDeleteSnippet,
  useExtractSignature,
  useReindexContacts,
  useRescanContact,
  useUpdateContact,
  useUploadSignature,
  useUploadSnippet,
} from "@/features/contacts/hooks/useContacts";
import { useRetryRuntimeWarmup, useRuntimeWarmup } from "@/features/runtime/hooks/useRuntimeWarmup";

type BannerState = {
  type: "success" | "error";
  message: string;
} | null;

function parseSignatureSource(signatureData?: string | null): string {
  if (!signatureData) {
    return "";
  }
  try {
    const parsed = JSON.parse(signatureData) as { source?: string };
    return typeof parsed.source === "string" ? parsed.source.toLowerCase() : "";
  } catch {
    return "";
  }
}

function statusClasses(status: string): string {
  const normalized = status.toLowerCase();
  if (normalized === "ready") {
    return "bg-emerald-500/15 text-emerald-700 border border-emerald-600/30";
  }
  if (normalized === "processing") {
    return "bg-amber-500/15 text-amber-700 border border-amber-600/30";
  }
  if (normalized === "failed") {
    return "bg-red-500/15 text-red-700 border border-red-600/30";
  }
  return "bg-[var(--bg-muted-pane)] text-[var(--text-secondary)] border border-[var(--border-subtle)]";
}

export function ContactsPage() {
  const [search, setSearch] = useState("");
  const [selectedContactID, setSelectedContactID] = useState<number | null>(null);
  const [banner, setBanner] = useState<BannerState>(null);
  const [showFilesPanel, setShowFilesPanel] = useState(false);

  const [createName, setCreateName] = useState("");
  const [createEmail, setCreateEmail] = useState("");
  const [createPhone, setCreatePhone] = useState("");

  const [draftName, setDraftName] = useState("");
  const [draftEmail, setDraftEmail] = useState("");
  const [draftPhone, setDraftPhone] = useState("");
  const [draftNotes, setDraftNotes] = useState("");

  const snippetInputRef = useRef<HTMLInputElement | null>(null);
  const signatureInputRef = useRef<HTMLInputElement | null>(null);
  const draftContactIDRef = useRef<number | null>(null);

  const contactsQuery = useContacts(search);
  const contacts = useMemo(() => contactsQuery.data?.contacts ?? [], [contactsQuery.data?.contacts]);

  useEffect(() => {
    if (selectedContactID == null) {
      return;
    }
    const stillExists = contacts.some((contact) => contact.id === selectedContactID);
    if (!stillExists) {
      setSelectedContactID(null);
    }
  }, [contacts, selectedContactID]);

  const selectedContactQuery = useContact(selectedContactID);
  const selectedContact = selectedContactQuery.data;
  const runtimeWarmupQuery = useRuntimeWarmup();
  const retryRuntimeWarmup = useRetryRuntimeWarmup();
  const runtimeWarmup = runtimeWarmupQuery.data;
  const signatureSource = useMemo(
    () => parseSignatureSource(selectedContact?.signature_data),
    [selectedContact?.signature_data],
  );
  const voiceSignatureRuntimePending =
    !!runtimeWarmup?.enabled &&
    !runtimeWarmup.voice_signatures_ready &&
    runtimeWarmup.state === "running";
  const voiceSignatureRuntimeFailed =
    !!runtimeWarmup?.enabled &&
    !runtimeWarmup.voice_signatures_ready &&
    runtimeWarmup.state === "failed";

  useEffect(() => {
    setShowFilesPanel(false);
  }, [selectedContactID]);

  useEffect(() => {
    if (!selectedContact) {
      setDraftName("");
      setDraftEmail("");
      setDraftPhone("");
      setDraftNotes("");
      draftContactIDRef.current = null;
      return;
    }
    if (draftContactIDRef.current === selectedContact.id) {
      return;
    }
    draftContactIDRef.current = selectedContact.id;
    setDraftName(selectedContact.name ?? "");
    setDraftEmail(selectedContact.email ?? "");
    setDraftPhone(selectedContact.phone ?? "");
    setDraftNotes(selectedContact.notes ?? "");
  }, [selectedContact]);

  const filesQuery = useContactFiles(selectedContactID, showFilesPanel);

  const createContact = useCreateContact();
  const updateContact = useUpdateContact(selectedContactID);
  const deleteContact = useDeleteContact();
  const reindexContacts = useReindexContacts();
  const uploadSnippet = useUploadSnippet(selectedContactID);
  const deleteSnippet = useDeleteSnippet(selectedContactID);
  const uploadSignature = useUploadSignature(selectedContactID);
  const deleteSignature = useDeleteSignature(selectedContactID);
  const extractSignature = useExtractSignature(selectedContactID);
  const rescanContact = useRescanContact(selectedContactID);

  const setErrorBanner = (error: unknown, fallback: string) => {
    const message = error instanceof Error ? error.message : fallback;
    setBanner({ type: "error", message });
  };

  const handleCreateContact = () => {
    if (!createName.trim()) {
      setBanner({ type: "error", message: "Name is required to create a contact." });
      return;
    }
    createContact.mutate(
      {
        name: createName.trim(),
        email: createEmail.trim() || null,
        phone: createPhone.trim() || null,
        notes: null,
      },
      {
        onSuccess: (contact) => {
          setCreateName("");
          setCreateEmail("");
          setCreatePhone("");
          setSelectedContactID(contact.id);
          setBanner({ type: "success", message: `Created contact "${contact.name}".` });
        },
        onError: (error) => {
          setErrorBanner(error, "Failed to create contact");
        },
      },
    );
  };

  const handleSaveContact = () => {
    if (!selectedContactID) {
      return;
    }
    if (!draftName.trim()) {
      setBanner({ type: "error", message: "Name is required." });
      return;
    }
    updateContact.mutate(
      {
        name: draftName.trim(),
        email: draftEmail.trim() || null,
        phone: draftPhone.trim() || null,
        notes: draftNotes.trim() || null,
      },
      {
        onSuccess: (updated) => {
          setBanner({ type: "success", message: `Saved "${updated.name}".` });
        },
        onError: (error) => {
          setErrorBanner(error, "Failed to save contact");
        },
      },
    );
  };

  const handleDeleteContact = () => {
    if (!selectedContactID || !selectedContact) {
      return;
    }
    const confirmed = window.confirm(`Delete contact "${selectedContact.name}"?`);
    if (!confirmed) {
      return;
    }
    deleteContact.mutate(selectedContactID, {
      onSuccess: () => {
        setSelectedContactID(null);
        setBanner({ type: "success", message: "Contact deleted." });
      },
      onError: (error) => {
        setErrorBanner(error, "Failed to delete contact");
      },
    });
  };

  const handleReindex = () => {
    reindexContacts.mutate(undefined, {
      onSuccess: () => {
        setBanner({ type: "success", message: "Contacts reindexed from vault files." });
      },
      onError: (error) => {
        setErrorBanner(error, "Failed to reindex contacts");
      },
    });
  };

  const handleSnippetSelected = (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file) {
      return;
    }
    uploadSnippet.mutate(file, {
      onSuccess: () => {
        setBanner({ type: "success", message: "Snippet uploaded." });
      },
      onError: (error) => {
        setErrorBanner(error, "Failed to upload snippet");
      },
    });
  };

  const handleSignatureSelected = (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file) {
      return;
    }
    uploadSignature.mutate(file, {
      onSuccess: () => {
        setBanner({ type: "success", message: "Voice signature imported." });
      },
      onError: (error) => {
        setErrorBanner(error, "Failed to import voice signature");
      },
    });
  };

  const showDetailPane = selectedContactID != null;

  return (
    <MainLayout header={<Header />}>
      <div className="obsidian-pane p-3 sm:p-5 mt-3">
        <div className="mb-4 sm:mb-6">
          <h1 className="text-2xl font-display font-bold text-[var(--text-primary)]">Contacts</h1>
          <p className="text-[var(--text-secondary)] text-sm sm:text-base">
            File-first contacts backed by your vault folders and signature artifacts.
          </p>
        </div>

        {banner && (
          <div
            className={`mb-4 rounded-[var(--radius-btn)] px-3 py-2 text-sm ${banner.type === "error" ? "bg-red-500/15 text-red-700 border border-red-600/30" : "bg-emerald-500/15 text-emerald-700 border border-emerald-600/30"}`}
          >
            {banner.message}
          </div>
        )}

        <div className="grid gap-4 lg:grid-cols-[340px_minmax(0,1fr)]">
          <section className={`${showDetailPane ? "hidden lg:block" : "block"} space-y-3`}>
            <div className="obsidian-inset p-3 space-y-3">
              <div className="flex items-center justify-between">
                <h2 className="text-sm font-semibold text-[var(--text-primary)]">People</h2>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={handleReindex}
                  disabled={reindexContacts.isPending}
                >
                  <RefreshCcw className={`h-4 w-4 ${reindexContacts.isPending ? "animate-spin" : ""}`} />
                  Reindex
                </Button>
              </div>
              <Input
                placeholder="Search contacts..."
                value={search}
                onChange={(event) => setSearch(event.target.value)}
              />
            </div>

            <div className="obsidian-inset p-3 space-y-2">
              <p className="text-xs uppercase tracking-wide text-[var(--text-tertiary)]">New Contact</p>
              <Input
                placeholder="Name *"
                value={createName}
                onChange={(event) => setCreateName(event.target.value)}
              />
              <Input
                placeholder="Email (optional)"
                value={createEmail}
                onChange={(event) => setCreateEmail(event.target.value)}
              />
              <Input
                placeholder="Phone (optional)"
                value={createPhone}
                onChange={(event) => setCreatePhone(event.target.value)}
              />
              <Button onClick={handleCreateContact} disabled={createContact.isPending}>
                <UserPlus className="h-4 w-4" />
                {createContact.isPending ? "Creating..." : "Create Contact"}
              </Button>
            </div>

            <div className="obsidian-pane p-2 max-h-[58vh] overflow-y-auto">
              {contactsQuery.isLoading ? (
                <p className="px-2 py-3 text-sm text-[var(--text-tertiary)]">Loading contacts...</p>
              ) : contacts.length === 0 ? (
                <p className="px-2 py-3 text-sm text-[var(--text-tertiary)]">No contacts found.</p>
              ) : (
                contacts.map((contact) => {
                  const selected = contact.id === selectedContactID;
                  return (
                    <button
                      key={contact.id}
                      type="button"
                      onClick={() => setSelectedContactID(contact.id)}
                      className={`w-full rounded-[var(--radius-btn)] border px-3 py-2 text-left transition ${selected ? "border-[var(--brand-solid)]/40 bg-[var(--bg-muted-pane)]" : "border-transparent hover:border-[var(--border-subtle)] hover:bg-[var(--bg-muted-pane)]/70"}`}
                    >
                      <div className="flex items-center justify-between gap-2">
                        <p className="truncate text-sm font-medium text-[var(--text-primary)]">{contact.name}</p>
                        <span className={`text-[10px] px-2 py-0.5 rounded-full ${statusClasses(contact.signature_status)}`}>
                          {contact.signature_status}
                        </span>
                      </div>
                      <p className="truncate text-xs text-[var(--text-secondary)] mt-1">{contact.email || contact.phone || "No contact info yet"}</p>
                    </button>
                  );
                })
              )}
            </div>
          </section>

          <section className={`${showDetailPane ? "block" : "hidden lg:block"}`}>
            {!selectedContact ? (
              <div className="obsidian-pane p-6 text-center text-[var(--text-secondary)]">
                Select a contact to edit details.
              </div>
            ) : (
              <div className="space-y-4">
                <div className="obsidian-pane p-3 sm:p-4">
                  <div className="mb-3 flex items-center justify-between gap-2">
                    <div className="flex items-center gap-2">
                      <Button
                        variant="ghost"
                        size="icon"
                        className="lg:hidden"
                        onClick={() => setSelectedContactID(null)}
                      >
                        <ArrowLeft className="h-4 w-4" />
                      </Button>
                      <h2 className="text-lg font-semibold text-[var(--text-primary)]">{selectedContact.name}</h2>
                    </div>
                    <span className={`text-xs px-2 py-1 rounded-full ${statusClasses(selectedContact.signature_status)}`}>
                      {selectedContact.signature_status}
                    </span>
                  </div>

                  {selectedContact.sync_error && (
                    <div className="mb-3 rounded-[var(--radius-btn)] border border-red-600/30 bg-red-500/15 px-3 py-2 text-sm text-red-700">
                      {selectedContact.sync_error}
                    </div>
                  )}

                  <div className="grid gap-3 md:grid-cols-2">
                    <div className="space-y-1">
                      <label className="text-xs text-[var(--text-tertiary)] uppercase tracking-wide">Name</label>
                      <Input value={draftName} onChange={(event) => setDraftName(event.target.value)} />
                    </div>
                    <div className="space-y-1">
                      <label className="text-xs text-[var(--text-tertiary)] uppercase tracking-wide">Email</label>
                      <Input value={draftEmail} onChange={(event) => setDraftEmail(event.target.value)} />
                    </div>
                    <div className="space-y-1 md:col-span-2">
                      <label className="text-xs text-[var(--text-tertiary)] uppercase tracking-wide">Phone</label>
                      <Input value={draftPhone} onChange={(event) => setDraftPhone(event.target.value)} />
                    </div>
                    <div className="space-y-1 md:col-span-2">
                      <label className="text-xs text-[var(--text-tertiary)] uppercase tracking-wide">Notes</label>
                      <Textarea
                        value={draftNotes}
                        onChange={(event) => setDraftNotes(event.target.value)}
                        className="min-h-[120px] bg-[var(--bg-main)]"
                      />
                    </div>
                  </div>

                  <div className="mt-4 flex flex-wrap gap-2">
                    <Button onClick={handleSaveContact} disabled={updateContact.isPending}>
                      <Save className="h-4 w-4" />
                      {updateContact.isPending ? "Saving..." : "Save"}
                    </Button>
                    <Button variant="outline" onClick={handleDeleteContact} disabled={deleteContact.isPending}>
                      <Trash2 className="h-4 w-4" />
                      {deleteContact.isPending ? "Deleting..." : "Delete"}
                    </Button>
                  </div>
                </div>

                <div className="grid gap-4 xl:grid-cols-2">
                  <div className="obsidian-pane p-3 sm:p-4 space-y-3">
                    <div className="flex items-center gap-2">
                      <FileAudio className="h-4 w-4 text-[var(--text-secondary)]" />
                      <h3 className="font-medium text-[var(--text-primary)]">Voice Snippet</h3>
                    </div>
                    <p className="text-sm text-[var(--text-secondary)]">
                      Upload a short voice sample for extraction workflows.
                    </p>
                    {selectedContact.voice_snippet_path && (
                      <p className="text-xs text-[var(--text-tertiary)] break-all">{selectedContact.voice_snippet_path}</p>
                    )}
                    <div className="flex flex-wrap gap-2">
                      <Button
                        variant="outline"
                        onClick={() => snippetInputRef.current?.click()}
                        disabled={uploadSnippet.isPending}
                      >
                        <Upload className="h-4 w-4" />
                        {uploadSnippet.isPending ? "Uploading..." : "Upload Snippet"}
                      </Button>
                      <Button
                        variant="outline"
                        onClick={() => deleteSnippet.mutate(undefined, {
                          onSuccess: () => setBanner({ type: "success", message: "Snippet removed." }),
                          onError: (error) => setErrorBanner(error, "Failed to remove snippet"),
                        })}
                        disabled={!selectedContact.voice_snippet_path || deleteSnippet.isPending}
                      >
                        <X className="h-4 w-4" />
                        Remove Snippet
                      </Button>
                    </div>
                    <input
                      ref={snippetInputRef}
                      type="file"
                      accept="audio/*"
                      className="hidden"
                      onChange={handleSnippetSelected}
                    />
                  </div>

                  <div className="obsidian-pane p-3 sm:p-4 space-y-3">
                    <div className="flex items-center gap-2">
                      <FileJson className="h-4 w-4 text-[var(--text-secondary)]" />
                      <h3 className="font-medium text-[var(--text-primary)]">Voice Signature</h3>
                    </div>
                    <p className="text-sm text-[var(--text-secondary)]">
                      Import an existing embedding JSON or extract from the snippet.
                    </p>
                    <div className="flex flex-wrap items-center gap-2 text-xs">
                      <span className={`px-2 py-1 rounded-full ${statusClasses(selectedContact.signature_status)}`}>
                        status: {selectedContact.signature_status}
                      </span>
                      {signatureSource && (
                        <span className="px-2 py-1 rounded-full border border-[var(--border-subtle)] text-[var(--text-secondary)] bg-[var(--bg-muted-pane)]">
                          source: {signatureSource}
                        </span>
                      )}
                    </div>
                    {selectedContact.signature_embedding_path && (
                      <p className="text-xs text-[var(--text-tertiary)] break-all">{selectedContact.signature_embedding_path}</p>
                    )}
                    {signatureSource === "manual" && selectedContact.voice_snippet_path && (
                      <p className="text-xs text-[var(--text-secondary)]">
                        Manual signature is authoritative until you run "Generate from Snippet".
                      </p>
                    )}
                    {voiceSignatureRuntimePending && (
                      <div className="rounded-[var(--radius-btn)] border border-sky-500/30 bg-sky-500/10 px-3 py-2 text-sm text-sky-800">
                        Quill is still preparing the local voice-signature runtime.
                        <div className="mt-1 text-xs text-sky-700">
                          {runtimeWarmup?.current_step_detail || "The default TitaNet model is downloading in the background."}
                        </div>
                      </div>
                    )}
                    {voiceSignatureRuntimeFailed && (
                      <div className="rounded-[var(--radius-btn)] border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm text-amber-800">
                        Voice-signature tools are not ready yet.
                        <div className="mt-1 text-xs text-amber-700">
                          {runtimeWarmup?.last_error || "Retry the local runtime warmup, then try extraction again."}
                        </div>
                        <div className="mt-2">
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => retryRuntimeWarmup.mutate(undefined, {
                              onError: () => { /* handled by banner */ },
                            })}
                            disabled={retryRuntimeWarmup.isPending}
                          >
                            <RefreshCcw className={`h-4 w-4 ${retryRuntimeWarmup.isPending ? "animate-spin" : ""}`} />
                            {retryRuntimeWarmup.isPending ? "Retrying..." : "Retry Runtime Setup"}
                          </Button>
                        </div>
                      </div>
                    )}
                    <div className="flex flex-wrap gap-2">
                      <Button
                        variant="outline"
                        onClick={() => signatureInputRef.current?.click()}
                        disabled={uploadSignature.isPending}
                      >
                        <Upload className="h-4 w-4" />
                        {uploadSignature.isPending ? "Importing..." : "Import Signature JSON"}
                      </Button>
                      <Button
                        variant="outline"
                        onClick={() => extractSignature.mutate(undefined, {
                          onSuccess: () => setBanner({ type: "success", message: "Signature extraction queued." }),
                          onError: (error) => setErrorBanner(error, "Failed to extract signature"),
                        })}
                        disabled={!selectedContact.voice_snippet_path || extractSignature.isPending}
                      >
                        <WandSparkles className="h-4 w-4" />
                        {extractSignature.isPending ? "Queuing..." : "Generate from Snippet"}
                      </Button>
                      <Button
                        variant="outline"
                        onClick={() => deleteSignature.mutate(undefined, {
                          onSuccess: () => setBanner({ type: "success", message: "Signature cleared." }),
                          onError: (error) => setErrorBanner(error, "Failed to clear signature"),
                        })}
                        disabled={!selectedContact.signature_embedding_path || deleteSignature.isPending}
                      >
                        <X className="h-4 w-4" />
                        Clear Signature
                      </Button>
                      {selectedContact.signature_status === "ready" && (
                        <Button
                          variant="outline"
                          onClick={() => rescanContact.mutate(undefined, {
                            onSuccess: () => setBanner({ type: "success", message: "Retroactive scan started." }),
                            onError: (error) => setErrorBanner(error, "Failed to start retroactive scan"),
                          })}
                          disabled={rescanContact.isPending}
                        >
                          <RefreshCcw className={`h-4 w-4 ${rescanContact.isPending ? "animate-spin" : ""}`} />
                          {rescanContact.isPending ? "Scanning..." : "Re-scan Past Transcriptions"}
                        </Button>
                      )}
                    </div>
                    <input
                      ref={signatureInputRef}
                      type="file"
                      accept=".json,application/json"
                      className="hidden"
                      onChange={handleSignatureSelected}
                    />
                  </div>
                </div>

                <div className="obsidian-pane p-3 sm:p-4">
                  <button
                    type="button"
                    className="w-full flex items-center justify-between rounded-[var(--radius-btn)] border border-[var(--border-subtle)] bg-[var(--bg-muted-pane)] px-3 py-2 text-left"
                    onClick={() => setShowFilesPanel((current) => !current)}
                  >
                    <span className="flex items-center gap-2 text-sm font-medium text-[var(--text-primary)]">
                      <FolderTree className="h-4 w-4" />
                      File Paths
                    </span>
                    <span className="text-xs text-[var(--text-secondary)]">{showFilesPanel ? "Hide" : "Show"}</span>
                  </button>
                  {showFilesPanel && (
                    <div className="mt-3 rounded-[var(--radius-btn)] border border-[var(--border-subtle)] bg-[var(--bg-main)] p-3 text-xs">
                      {filesQuery.isLoading ? (
                        <p className="text-[var(--text-tertiary)]">Loading file paths...</p>
                      ) : filesQuery.data ? (
                        <div className="space-y-2 text-[var(--text-secondary)]">
                          <p><span className="font-semibold text-[var(--text-primary)]">Note:</span> <span className="break-all">{filesQuery.data.note_path}</span></p>
                          <p><span className="font-semibold text-[var(--text-primary)]">Note (abs):</span> <span className="break-all">{filesQuery.data.note_abs_path || "-"}</span></p>
                          <p><span className="font-semibold text-[var(--text-primary)]">Snippet:</span> <span className="break-all">{filesQuery.data.voice_snippet_path || "-"}</span></p>
                          <p><span className="font-semibold text-[var(--text-primary)]">Snippet (abs):</span> <span className="break-all">{filesQuery.data.voice_snippet_abs_path || "-"}</span></p>
                          <p><span className="font-semibold text-[var(--text-primary)]">Signature:</span> <span className="break-all">{filesQuery.data.signature_embedding_path || "-"}</span></p>
                          <p><span className="font-semibold text-[var(--text-primary)]">Signature (abs):</span> <span className="break-all">{filesQuery.data.signature_embedding_abs_path || "-"}</span></p>
                        </div>
                      ) : (
                        <p className="text-red-700">Unable to load file diagnostics.</p>
                      )}
                    </div>
                  )}
                </div>
              </div>
            )}
          </section>
        </div>
      </div>
    </MainLayout>
  );
}
