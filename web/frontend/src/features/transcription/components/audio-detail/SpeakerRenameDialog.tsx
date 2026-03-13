import React, { useState, useEffect, useCallback, useMemo } from 'react';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Card, CardContent } from '@/components/ui/card';
import { Popover, PopoverAnchor, PopoverContent } from '@/components/ui/popover';
import { Loader2, Users, Save, X, Sparkles } from 'lucide-react';
import { useToast } from '@/components/ui/toast';
import { useAuth } from "@/features/auth/hooks/useAuth";
import { useQueryClient } from "@tanstack/react-query";
import { useContacts } from "@/features/contacts/hooks/useContacts";
import type { Contact } from "@/features/contacts/types";
import type { SpeakerMapping, SpeakerMappingsUpdateResponse } from "@/features/transcription/hooks/useTranscriptionSpeakers";
import type { SpeakerIdentificationEvent, SpeakerSuggestion } from "@/features/transcription/hooks/useTranscriptionEvents";

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
  const [isLoading, setIsLoading] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [activeSpeaker, setActiveSpeaker] = useState<string | null>(null);
  const [highlightedSuggestionIndex, setHighlightedSuggestionIndex] = useState(0);
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

      // Initialize with existing mappings
      existingMappings.forEach(mapping => {
        mappingObj[mapping.original_speaker] = mapping.custom_name;
      });

      // Add any speakers from the transcript that don't have mappings yet
      initialSpeakers.forEach(speaker => {
        if (!mappingObj[speaker]) {
          mappingObj[speaker] = speaker; // Default to original name
        }
      });

      setSpeakerMappings(mappingObj);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to fetch speaker mappings');

      // Initialize with default mappings if fetch fails
      const defaultMappings: Record<string, string> = {};
      initialSpeakers.forEach(speaker => {
        defaultMappings[speaker] = speaker;
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

  const handleSpeakerInputKeyDown = useCallback((event: React.KeyboardEvent<HTMLInputElement>, originalSpeaker: string) => {
    const suggestions = getContactSuggestions(speakerMappings[originalSpeaker] ?? "", originalSpeaker);
    if (suggestions.length === 0) {
      return;
    }

    if (event.key === "ArrowDown") {
      event.preventDefault();
      setActiveSpeaker(originalSpeaker);
      setHighlightedSuggestionIndex((prev) => (prev + 1) % suggestions.length);
      return;
    }

    if (event.key === "ArrowUp") {
      event.preventDefault();
      setActiveSpeaker(originalSpeaker);
      setHighlightedSuggestionIndex((prev) => (prev - 1 + suggestions.length) % suggestions.length);
      return;
    }

    if (event.key === "Enter") {
      if (activeSpeaker !== originalSpeaker) {
        return;
      }
      event.preventDefault();
      const selected = suggestions[highlightedSuggestionIndex] ?? suggestions[0];
      if (selected) {
        applyContactSuggestion(originalSpeaker, selected);
      }
      return;
    }

    if (event.key === "Escape") {
      setActiveSpeaker(null);
      setHighlightedSuggestionIndex(0);
    }
  }, [activeSpeaker, applyContactSuggestion, getContactSuggestions, highlightedSuggestionIndex, speakerMappings]);

  const saveSpeakerMappings = async () => {
    setIsSaving(true);
    setError(null);

    try {
      // Convert mappings to API format
      const mappingsArray = Object.entries(speakerMappings).map(([original_speaker, custom_name]) => ({
        original_speaker,
        custom_name,
      }));

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

  useEffect(() => {
    if (!open) {
      setActiveSpeaker(null);
      setHighlightedSuggestionIndex(0);
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
                          {speaker}
                        </Label>
                        {voiceSuggestion && (
                          <button
                            type="button"
                            onClick={() => {
                              setSpeakerMappings((prev) => ({
                                ...prev,
                                [speaker]: voiceSuggestion.contact_name,
                              }));
                            }}
                            className="inline-flex items-center gap-1 text-xs px-1.5 py-0.5 rounded bg-[var(--brand-solid)]/10 text-[var(--brand-solid)] hover:bg-[var(--brand-solid)]/20 transition-colors"
                            title={`Voice match: ${Math.round(voiceSuggestion.score * 100)}% confidence`}
                          >
                            <Sparkles className="h-3 w-3" />
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
                              setActiveSpeaker(speaker);
                              setHighlightedSuggestionIndex(0);
                            }}
                            onClick={() => {
                              setActiveSpeaker(speaker);
                              setHighlightedSuggestionIndex(0);
                            }}
                            onKeyDown={(event) => handleSpeakerInputKeyDown(event, speaker)}
                            placeholder={`Enter custom name for ${speaker}`}
                            className="transition-all duration-200 focus:ring-2 focus:ring-primary/20"
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
                            ) : contacts.length === 0 ? (
                              <div className="px-3 py-2 text-xs text-[var(--text-tertiary)]">
                                No contacts found. Add contacts first.
                              </div>
                            ) : (
                              suggestions.map((contact, index) => (
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
                              ))
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
            disabled={isSaving || speakers.length === 0}
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
