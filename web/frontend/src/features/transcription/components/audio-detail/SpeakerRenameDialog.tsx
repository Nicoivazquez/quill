import React, { useState, useEffect, useCallback, useMemo } from 'react';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Card, CardContent } from '@/components/ui/card';
import { Popover, PopoverAnchor, PopoverContent } from '@/components/ui/popover';
import { Loader2, Users, Save, X, Sparkles, Check, UserPlus } from 'lucide-react';
import { useToast } from '@/components/ui/toast';
import { useAuth } from "@/features/auth/hooks/useAuth";
import { useQueryClient } from "@tanstack/react-query";
import { useContacts, useCreateContact } from "@/features/contacts/hooks/useContacts";
import type { Contact } from "@/features/contacts/types";
import type { SpeakerMapping, SpeakerMappingsUpdateResponse } from "@/features/transcription/hooks/useTranscriptionSpeakers";
import { usePromoteSpeakerSuggestion } from "@/features/transcription/hooks/useTranscriptionSpeakers";
import type { SpeakerIdentificationEvent, SpeakerSuggestion } from "@/features/transcription/hooks/useTranscriptionEvents";
import { formatSpeakerLabel } from "@/lib/speaker-utils";

interface SpeakerRenameDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  transcriptionId: string;
  onSpeakerMappingsUpdate: (mappings: SpeakerMapping[]) => void;
  initialSpeakers?: string[]; // Detected speakers from transcript
}

const MAX_CONTACT_SUGGESTIONS = 8;

function normalize(value: string | null | undefined): string {
  return (value ?? "").trim().toLowerCase();
}

const SpeakerRenameDialog: React.FC<SpeakerRenameDialogProps> = ({
  open,
  onOpenChange,
  transcriptionId,
  onSpeakerMappingsUpdate,
  initialSpeakers = [],
}) => {
  const { getAuthHeaders } = useAuth();
  const { toast } = useToast();
  const queryClient = useQueryClient();
  const [speakerMappings, setSpeakerMappings] = useState<Record<string, string>>({});
  const [mappingDetails, setMappingDetails] = useState<Map<string, SpeakerMapping>>(new Map());
  const [isLoading, setIsLoading] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [activeSpeaker, setActiveSpeaker] = useState<string | null>(null);
  const [highlightedSuggestionIndex, setHighlightedSuggestionIndex] = useState(0);
  const [promotedSpeakers, setPromotedSpeakers] = useState<Set<string>>(new Set());
  const promoteMutation = usePromoteSpeakerSuggestion();
  const createContactMutation = useCreateContact();
  const contactsQuery = useContacts("", open);
  const contacts = useMemo(() => contactsQuery.data?.contacts ?? [], [contactsQuery.data?.contacts]);

  // Read auto-label suggestions from React Query cache (populated by SSE)
  const autoLabelData = queryClient.getQueryData<SpeakerIdentificationEvent>(
    ['speakerSuggestions', transcriptionId],
  );

  // Build a lookup: speaker label -> best suggestion
  const voiceSuggestions = useMemo(() => {
    const map = new Map<string, SpeakerSuggestion>();
    if (!autoLabelData) return map;
    // Suggestions (tier="suggest") are the ones we show as actionable chips
    for (const s of autoLabelData.suggestions) {
      map.set(s.speaker, s);
    }
    return map;
  }, [autoLabelData]);
  const topContacts = useMemo(
    () => [...contacts].sort((a, b) => a.name.localeCompare(b.name)).slice(0, MAX_CONTACT_SUGGESTIONS),
    [contacts],
  );

  const fetchSpeakerMappings = useCallback(async () => {
    setIsLoading(true);
    setError(null);

    try {
      const response = await fetch(`/api/v1/transcription/${transcriptionId}/speakers`, {
        headers: { ...getAuthHeaders() },
      });

      if (!response.ok) {
        throw new Error(`Failed to fetch speaker mappings: ${response.statusText}`);
      }

      const existingMappings: SpeakerMapping[] = await response.json();

      // Create a mapping object from the response
      const mappingObj: Record<string, string> = {};
      const detailsMap = new Map<string, SpeakerMapping>();
      const alreadyPromoted = new Set<string>();

      // Initialize with existing mappings and preserve confidence metadata
      existingMappings.forEach(mapping => {
        // If custom_name is just the raw label (legacy auto-populated data), treat as unset
        // so the placeholder shows the friendly "Speaker A" / "Speaker B" format
        const isRawLabel = mapping.custom_name === mapping.original_speaker ||
          /^speaker[_ ]\d+$/i.test(mapping.custom_name);
        mappingObj[mapping.original_speaker] = isRawLabel ? '' : mapping.custom_name;
        detailsMap.set(mapping.original_speaker, mapping);
        if (mapping.match_source === 'suggestion_promoted') {
          alreadyPromoted.add(mapping.original_speaker);
        }
      });

      // Add any speakers from the transcript that don't have mappings yet
      initialSpeakers.forEach(speaker => {
        if (!mappingObj[speaker]) {
          mappingObj[speaker] = ''; // Empty so placeholder shows friendly label
        }
      });

      setSpeakerMappings(mappingObj);
      setMappingDetails(detailsMap);
      setPromotedSpeakers(alreadyPromoted);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to fetch speaker mappings');

      // Initialize with default mappings if fetch fails
      const defaultMappings: Record<string, string> = {};
      initialSpeakers.forEach(speaker => {
        defaultMappings[speaker] = '';
      });
      setSpeakerMappings(defaultMappings);
    } finally {
      setIsLoading(false);
    }
  }, [transcriptionId, getAuthHeaders, initialSpeakers]);

  // Initialize speaker mappings when dialog opens
  useEffect(() => {
    if (open && transcriptionId) {
      fetchSpeakerMappings();
    }
  }, [open, transcriptionId, fetchSpeakerMappings]);

  const handleSpeakerNameChange = (originalSpeaker: string, customName: string) => {
    setSpeakerMappings(prev => ({
      ...prev,
      [originalSpeaker]: customName,
    }));
    setActiveSpeaker(originalSpeaker);
    setHighlightedSuggestionIndex(0);
  };

  const getContactSuggestions = useCallback((query: string, originalSpeaker: string): Contact[] => {
    if (contacts.length === 0) {
      return [];
    }

    const normalizedQuery = normalize(query);
    const normalizedOriginalSpeaker = normalize(originalSpeaker);

    // The default seeded value in each input is the detected diarization speaker label.
    // Treat that as "empty search" so suggestions show for every speaker row.
    if (!normalizedQuery || normalizedQuery === normalizedOriginalSpeaker) {
      return topContacts;
    }

    const rankedMatches = contacts
      .map((contact) => {
        const name = normalize(contact.name);
        const email = normalize(contact.email);
        const phone = normalize(contact.phone);

        let score = Number.MAX_SAFE_INTEGER;
        if (name === normalizedQuery) {
          score = 0;
        } else if (name.startsWith(normalizedQuery)) {
          score = 1;
        } else if (name.includes(normalizedQuery)) {
          score = 2;
        } else if (email.startsWith(normalizedQuery)) {
          score = 3;
        } else if (email.includes(normalizedQuery)) {
          score = 4;
        } else if (phone.includes(normalizedQuery)) {
          score = 5;
        } else {
          return null;
        }

        return { contact, score };
      })
      .filter((item): item is { contact: Contact; score: number } => item !== null)
      .sort((a, b) => a.score - b.score || a.contact.name.localeCompare(b.contact.name))
      .map((item) => item.contact);

    if (rankedMatches.length === 0) {
      return topContacts;
    }

    return rankedMatches.slice(0, MAX_CONTACT_SUGGESTIONS);
  }, [contacts, topContacts]);

  const applyContactSuggestion = useCallback((originalSpeaker: string, contact: Contact) => {
    setSpeakerMappings((prev) => ({
      ...prev,
      [originalSpeaker]: contact.name,
    }));
    setActiveSpeaker(null);
    setHighlightedSuggestionIndex(0);
  }, []);

  // Returns true when the typed query is a novel name not matching any existing contact
  const isNewContactCandidate = useCallback((query: string, originalSpeaker: string): boolean => {
    const trimmed = query.trim();
    if (!trimmed) return false;
    // Don't show create option if user hasn't changed from the original speaker label
    if (normalize(trimmed) === normalize(originalSpeaker)) return false;
    // Don't show if a contact with this exact name already exists
    return !contacts.some((c) => normalize(c.name) === normalize(trimmed));
  }, [contacts]);

  const handleCreateContact = useCallback((originalSpeaker: string, name: string) => {
    const trimmed = name.trim();
    if (!trimmed) return;
    createContactMutation.mutate(
      { name: trimmed, phone: null, email: null, notes: null },
      {
        onSuccess: (contact) => {
          setSpeakerMappings((prev) => ({
            ...prev,
            [originalSpeaker]: contact.name,
          }));
          setActiveSpeaker(null);
          setHighlightedSuggestionIndex(0);
          toast({ title: `Contact "${contact.name}" created` });
        },
        onError: (err) => {
          toast({ title: "Failed to create contact", description: err.message });
        },
      },
    );
  }, [createContactMutation, toast]);

  const handleSpeakerInputKeyDown = useCallback((event: React.KeyboardEvent<HTMLInputElement>, originalSpeaker: string) => {
    const suggestions = getContactSuggestions(speakerMappings[originalSpeaker] ?? "", originalSpeaker);
    const query = speakerMappings[originalSpeaker] ?? "";
    const showCreate = isNewContactCandidate(query, originalSpeaker);
    const totalItems = suggestions.length + (showCreate ? 1 : 0);
    if (totalItems === 0) return;

    if (event.key === "ArrowDown") {
      event.preventDefault();
      setActiveSpeaker(originalSpeaker);
      setHighlightedSuggestionIndex((prev) => (prev + 1) % totalItems);
      return;
    }

    if (event.key === "ArrowUp") {
      event.preventDefault();
      setActiveSpeaker(originalSpeaker);
      setHighlightedSuggestionIndex((prev) => (prev - 1 + totalItems) % totalItems);
      return;
    }

    if (event.key === "Enter") {
      if (activeSpeaker !== originalSpeaker) return;
      event.preventDefault();
      // If the highlighted index is past all suggestions, it's the "Create" option
      if (showCreate && highlightedSuggestionIndex === suggestions.length) {
        handleCreateContact(originalSpeaker, query);
      } else {
        const selected = suggestions[highlightedSuggestionIndex] ?? suggestions[0];
        if (selected) {
          applyContactSuggestion(originalSpeaker, selected);
        }
      }
      return;
    }

    if (event.key === "Escape") {
      setActiveSpeaker(null);
      setHighlightedSuggestionIndex(0);
    }
  }, [activeSpeaker, applyContactSuggestion, getContactSuggestions, handleCreateContact, highlightedSuggestionIndex, isNewContactCandidate, speakerMappings]);

  const saveSpeakerMappings = async () => {
    setIsSaving(true);
    setError(null);

    try {
      // Convert mappings to API format, excluding locked speakers and empty names
      const mappingsArray = Object.entries(speakerMappings)
        .filter(([original_speaker, custom_name]) => !isLockedSpeaker(original_speaker) && custom_name.trim() !== '')
        .map(([original_speaker, custom_name]) => ({
          original_speaker,
          custom_name,
        }));

      // If all speakers were promoted, just close — no bulk POST needed
      if (mappingsArray.length === 0) {
        onOpenChange(false);
        return;
      }

      const response = await fetch(`/api/v1/transcription/${transcriptionId}/speakers`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...getAuthHeaders() },
        body: JSON.stringify({
          mappings: mappingsArray,
        }),
      });

      if (!response.ok) {
        throw new Error(`Failed to save speaker mappings: ${response.statusText}`);
      }

      const payload: SpeakerMappingsUpdateResponse = await response.json();
      onSpeakerMappingsUpdate(payload.mappings);

      if (payload.contact_bootstrap.started_count > 0) {
        const started = payload.contact_bootstrap.started_count;
        const created = payload.contact_bootstrap.created_count;
        const createdText = created > 0
          ? `${created} new ${created === 1 ? "contact" : "contacts"} created. `
          : "";

        toast({
          title: `Voice bootstrap started for ${started} ${started === 1 ? "contact" : "contacts"}`,
          description: `${createdText}Snippet extraction and voice signature generation are running in the background.`,
        });
      }

      onOpenChange(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save speaker mappings');
    } finally {
      setIsSaving(false);
    }
  };

  const speakers = Object.keys(speakerMappings).sort();

  // A speaker is "locked" if it was auto-assigned or previously promoted
  const isLockedSpeaker = useCallback((speaker: string) => {
    if (promotedSpeakers.has(speaker)) return true;
    const detail = mappingDetails.get(speaker);
    return detail?.match_source === 'auto';
  }, [promotedSpeakers, mappingDetails]);

  useEffect(() => {
    if (!open) {
      setActiveSpeaker(null);
      setHighlightedSuggestionIndex(0);
      setPromotedSpeakers(new Set());
      setMappingDetails(new Map());
    }
  }, [open]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Users className="h-5 w-5" />
            Rename Speakers
          </DialogTitle>
        </DialogHeader>

        {isLoading ? (
          <div className="flex items-center justify-center py-8">
            <Loader2 className="h-6 w-6 animate-spin" />
            <span className="ml-2 text-sm text-muted-foreground">Loading speakers...</span>
          </div>
        ) : (
          <div className="space-y-4">
            {error && (
              <div className="p-3 rounded-md bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800">
                <p className="text-sm text-red-600 dark:text-red-400">{error}</p>
              </div>
            )}

            {speakers.length === 0 ? (
              <Card>
                <CardContent className="pt-6 text-center text-muted-foreground">
                  <Users className="h-8 w-8 mx-auto mb-2 opacity-50" />
                  <p>No speakers found with diarization enabled.</p>
                </CardContent>
              </Card>
            ) : (
              <div className="space-y-3 max-h-60 overflow-y-auto">
                {speakers.map((speaker) => {
                  const suggestions = getContactSuggestions(speakerMappings[speaker] ?? "", speaker);
                  const voiceSuggestion = voiceSuggestions.get(speaker);

                  return (
                    <div
                      key={speaker}
                      className="space-y-1"
                    >
                      <div className="flex items-center gap-2">
                        <Label htmlFor={`speaker-${speaker}`} className="text-xs font-medium text-muted-foreground">
                          {formatSpeakerLabel(speaker)}
                        </Label>
                        {promotedSpeakers.has(speaker) ? (
                          <span className="inline-flex items-center gap-1 text-xs px-1.5 py-0.5 rounded bg-green-500/10 text-green-600 dark:text-green-400" data-testid={`badge-matched-${speaker}`}>
                            <Check className="h-3 w-3" />
                            Matched{mappingDetails.get(speaker)?.confidence_score ? ` ${Math.round(mappingDetails.get(speaker)!.confidence_score! * 100)}%` : ''}
                          </span>
                        ) : mappingDetails.get(speaker)?.match_source === 'auto' ? (
                          <span className="inline-flex items-center gap-1 text-xs px-1.5 py-0.5 rounded bg-blue-500/10 text-blue-600 dark:text-blue-400" data-testid={`badge-auto-${speaker}`}>
                            <Sparkles className="h-3 w-3" />
                            Auto {Math.round((mappingDetails.get(speaker)?.confidence_score ?? 0) * 100)}%
                          </span>
                        ) : voiceSuggestion && (
                          <button
                            type="button"
                            disabled={promoteMutation.isPending && promoteMutation.variables?.originalSpeaker === speaker}
                            onClick={() => {
                              promoteMutation.mutate(
                                {
                                  transcriptionId,
                                  originalSpeaker: speaker,
                                  contactId: voiceSuggestion.contact_id,
                                  contactName: voiceSuggestion.contact_name,
                                  score: voiceSuggestion.score,
                                },
                                {
                                  onSuccess: (data) => {
                                    setSpeakerMappings((prev) => ({
                                      ...prev,
                                      [speaker]: voiceSuggestion.contact_name,
                                    }));
                                    setPromotedSpeakers((prev) => new Set(prev).add(speaker));
                                    onSpeakerMappingsUpdate(data.mappings);
                                    toast({
                                      title: `Speaker assigned to ${voiceSuggestion.contact_name}`,
                                      description: `Voice match with ${Math.round(voiceSuggestion.score * 100)}% confidence`,
                                    });
                                  },
                                },
                              );
                            }}
                            className="inline-flex items-center gap-1 text-xs px-1.5 py-0.5 rounded bg-[var(--brand-solid)]/10 text-[var(--brand-solid)] hover:bg-[var(--brand-solid)]/20 transition-colors"
                            title={`Voice match: ${Math.round(voiceSuggestion.score * 100)}% confidence — click to apply`}
                          >
                            {promoteMutation.isPending && promoteMutation.variables?.originalSpeaker === speaker ? (
                              <Loader2 className="h-3 w-3 animate-spin" />
                            ) : (
                              <Sparkles className="h-3 w-3" />
                            )}
                            {voiceSuggestion.contact_name} ({Math.round(voiceSuggestion.score * 100)}%)
                          </button>
                        )}
                      </div>
                      <Popover
                        open={activeSpeaker === speaker}
                        onOpenChange={(nextOpen) => {
                          setActiveSpeaker((current) => {
                            if (nextOpen) {
                              return speaker;
                            }
                            return current === speaker ? null : current;
                          });
                          if (!nextOpen) {
                            setHighlightedSuggestionIndex(0);
                          }
                        }}
                      >
                        <PopoverAnchor asChild>
                          <Input
                            id={`speaker-${speaker}`}
                            value={speakerMappings[speaker] || ''}
                            onChange={(e) => handleSpeakerNameChange(speaker, e.target.value)}
                            onFocus={() => {
                              if (!isLockedSpeaker(speaker)) {
                                setActiveSpeaker(speaker);
                                setHighlightedSuggestionIndex(0);
                              }
                            }}
                            onClick={() => {
                              if (!isLockedSpeaker(speaker)) {
                                setActiveSpeaker(speaker);
                                setHighlightedSuggestionIndex(0);
                              }
                            }}
                            onKeyDown={(event) => handleSpeakerInputKeyDown(event, speaker)}
                            placeholder={`Enter custom name for ${formatSpeakerLabel(speaker)}`}
                            readOnly={isLockedSpeaker(speaker)}
                            className={`transition-all duration-200 focus:ring-2 focus:ring-primary/20 ${isLockedSpeaker(speaker) ? 'opacity-60 cursor-default' : ''}`}
                          />
                        </PopoverAnchor>
                        <PopoverContent
                          align="start"
                          sideOffset={6}
                          onOpenAutoFocus={(event) => event.preventDefault()}
                          className="w-[var(--radix-popover-trigger-width)] p-0"
                        >
                          <div className="max-h-44 overflow-y-auto py-1">
                            {contactsQuery.isLoading ? (
                              <div className="px-3 py-2 text-xs text-[var(--text-tertiary)]">
                                Loading contacts...
                              </div>
                            ) : (
                              <>
                                {suggestions.map((contact, index) => (
                                  <button
                                    type="button"
                                    role="option"
                                    aria-selected={index === highlightedSuggestionIndex}
                                    key={contact.id}
                                    onMouseEnter={() => setHighlightedSuggestionIndex(index)}
                                    onMouseDown={(event) => {
                                      event.preventDefault();
                                      applyContactSuggestion(speaker, contact);
                                    }}
                                    className={`w-full px-3 py-2 text-left transition-colors ${index === highlightedSuggestionIndex
                                      ? "bg-[var(--bg-muted-pane)]"
                                      : "hover:bg-[var(--bg-muted-pane)]/70"
                                      }`}
                                  >
                                    <div className="text-sm font-medium text-[var(--text-primary)]">
                                      {contact.name}
                                    </div>
                                    {(contact.email || contact.phone) && (
                                      <div className="text-xs text-[var(--text-secondary)]">
                                        {contact.email || contact.phone}
                                      </div>
                                    )}
                                  </button>
                                ))}
                                {isNewContactCandidate(speakerMappings[speaker] ?? "", speaker) && (() => {
                                  const createIndex = suggestions.length;
                                  const trimmedName = (speakerMappings[speaker] ?? "").trim();
                                  return (
                                    <>
                                      {suggestions.length > 0 && (
                                        <div className="border-t border-[var(--border-subtle)] my-1" />
                                      )}
                                      <button
                                        type="button"
                                        role="option"
                                        aria-selected={createIndex === highlightedSuggestionIndex}
                                        onMouseEnter={() => setHighlightedSuggestionIndex(createIndex)}
                                        onMouseDown={(event) => {
                                          event.preventDefault();
                                          handleCreateContact(speaker, trimmedName);
                                        }}
                                        disabled={createContactMutation.isPending}
                                        className={`w-full px-3 py-2 text-left transition-colors flex items-center gap-2 ${createIndex === highlightedSuggestionIndex
                                          ? "bg-[var(--bg-muted-pane)]"
                                          : "hover:bg-[var(--bg-muted-pane)]/70"
                                          }`}
                                      >
                                        {createContactMutation.isPending ? (
                                          <Loader2 className="h-4 w-4 animate-spin text-[var(--brand-solid)] shrink-0" />
                                        ) : (
                                          <UserPlus className="h-4 w-4 text-[var(--brand-solid)] shrink-0" />
                                        )}
                                        <span className="text-sm text-[var(--text-primary)]">
                                          Create "<span className="font-medium">{trimmedName}</span>"
                                        </span>
                                      </button>
                                    </>
                                  );
                                })()}
                                {suggestions.length === 0 && !isNewContactCandidate(speakerMappings[speaker] ?? "", speaker) && contacts.length === 0 && (
                                  <div className="px-3 py-2 text-xs text-[var(--text-tertiary)]">
                                    No contacts yet. Type a name to create one.
                                  </div>
                                )}
                              </>
                            )}
                          </div>
                        </PopoverContent>
                      </Popover>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        )}

        <DialogFooter className="gap-2">
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={isSaving}>
            <X className="h-4 w-4 mr-1" />
            Cancel
          </Button>
          <Button
            onClick={saveSpeakerMappings}
            disabled={isSaving || speakers.length === 0 || createContactMutation.isPending}
            className="min-w-[100px]"
          >
            {isSaving ? (
              <>
                <Loader2 className="h-4 w-4 mr-1 animate-spin" />
                Saving...
              </>
            ) : (
              <>
                <Save className="h-4 w-4 mr-1" />
                Save
              </>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
};

export default SpeakerRenameDialog;
