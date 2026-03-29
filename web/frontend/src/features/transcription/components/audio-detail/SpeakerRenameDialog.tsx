import React, { useState, useEffect, useCallback, useMemo } from 'react';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Card, CardContent } from '@/components/ui/card';
import { Popover, PopoverAnchor, PopoverContent } from '@/components/ui/popover';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Loader2, Users, Save, X, Sparkles, Check, UserPlus, RefreshCw, ChevronDown } from 'lucide-react';
import { useToast } from '@/components/ui/toast';
import { useAuth } from "@/features/auth/hooks/useAuth";
import { useQueryClient } from "@tanstack/react-query";
import { useContacts, useCreateContact } from "@/features/contacts/hooks/useContacts";
import type { Contact } from "@/features/contacts/types";
import type { SpeakerMapping, SpeakerMappingsUpdateResponse } from "@/features/transcription/hooks/useTranscriptionSpeakers";
import { usePromoteSpeakerSuggestion, useSpeakerSuggestions, useDismissSpeakerSuggestion } from "@/features/transcription/hooks/useTranscriptionSpeakers";
import { formatSpeakerLabel } from "@/lib/speaker-utils";

interface SpeakerRenameDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  transcriptionId: string;
  onSpeakerMappingsUpdate: (mappings: SpeakerMapping[]) => void;
  initialSpeakers?: string[]; // Detected speakers from transcript
}

const MAX_CONTACT_SUGGESTIONS = 8;

const MODEL_FAMILIES = [
  { value: "mlx_whisper", label: "MLX Whisper (Apple Silicon)" },
  { value: "whisper", label: "WhisperX" },
  { value: "whisper_cpp", label: "Whisper.cpp" },
  { value: "nvidia_parakeet", label: "NVIDIA Parakeet" },
  { value: "nvidia_canary", label: "NVIDIA Canary" },
  { value: "mistral_voxtral", label: "Mistral Voxtral" },
  { value: "openai", label: "OpenAI (Cloud)" },
] as const;

const WHISPER_MODELS = ["small", "small.en", "medium", "medium.en", "large-v3", "large-v3-turbo"];

function getModelsForFamily(family: string): string[] {
  switch (family) {
    case "whisper":
    case "mlx_whisper":
    case "whisper_cpp":
      return WHISPER_MODELS;
    case "nvidia_parakeet":
      return ["parakeet-ctc-1.1b", "parakeet-tdt-1.1b"];
    case "nvidia_canary":
      return ["canary-1b"];
    case "mistral_voxtral":
      return ["mistral-voxtral-latest"];
    case "openai":
      return ["whisper-1"];
    default:
      return WHISPER_MODELS;
  }
}

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
  const [retranscribeOpen, setRetranscribeOpen] = useState(false);
  const [retranscribeFamily, setRetranscribeFamily] = useState("mlx_whisper");
  const [retranscribeModel, setRetranscribeModel] = useState("large-v3-turbo");
  const [retranscribeNumSpeakers, setRetranscribeNumSpeakers] = useState("");
  const [retranscribeDiarizeMode, setRetranscribeDiarizeMode] = useState<"local" | "pyannote">("local");
  const [isRetranscribing, setIsRetranscribing] = useState(false);
  const promoteMutation = usePromoteSpeakerSuggestion();
  const dismissMutation = useDismissSpeakerSuggestion();
  const createContactMutation = useCreateContact();
  const contactsQuery = useContacts("", open);
  const contacts = useMemo(() => contactsQuery.data?.contacts ?? [], [contactsQuery.data?.contacts]);

  // Fetch pending suggestions from DB-backed API (persisted, not ephemeral SSE)
  const suggestionsQuery = useSpeakerSuggestions(transcriptionId, open);

  // Build a lookup: speaker label -> best pending suggestion
  const voiceSuggestions = useMemo(() => {
    const map = new Map<string, SpeakerMapping>();
    if (!suggestionsQuery.data) return map;
    for (const s of suggestionsQuery.data) {
      map.set(s.original_speaker, s);
    }
    return map;
  }, [suggestionsQuery.data]);
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

  const handleRetranscribe = async () => {
    setIsRetranscribing(true);
    try {
      const numSpeakers = retranscribeNumSpeakers ? parseInt(retranscribeNumSpeakers, 10) : undefined;
      const isSortformer = retranscribeDiarizeMode === "local";
      const diarizeModel = isSortformer ? "nvidia_sortformer" : "pyannote";

      const params: Record<string, unknown> = {
        model_family: retranscribeFamily,
        model: retranscribeModel,
        diarize: true,
        diarize_model: diarizeModel,
      };

      if (numSpeakers && numSpeakers > 0) {
        // Sortformer only supports max_speakers; Pyannote supports both
        if (isSortformer) {
          params.max_speakers = numSpeakers;
        } else {
          params.min_speakers = numSpeakers;
          params.max_speakers = numSpeakers;
        }
      }

      const response = await fetch(`/api/v1/transcription/${transcriptionId}/start`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...getAuthHeaders() },
        body: JSON.stringify(params),
      });

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw new Error(data.error || `Re-transcription failed: ${response.statusText}`);
      }

      // Invalidate queries so polling picks up the new processing status
      queryClient.invalidateQueries({ queryKey: ["audio", transcriptionId] });
      queryClient.invalidateQueries({ queryKey: ["audioFiles"] });

      toast({ title: "Re-transcription started", description: "The audio is being re-transcribed with updated settings." });
      onOpenChange(false);
    } catch (err) {
      toast({ title: "Re-transcription failed", description: err instanceof Error ? err.message : "Unknown error" });
    } finally {
      setIsRetranscribing(false);
    }
  };

  const speakers = Object.keys(speakerMappings).sort();

  // A speaker is "locked" if it was auto-assigned or previously promoted
  const isLockedSpeaker = useCallback((speaker: string) => {
    if (promotedSpeakers.has(speaker)) return true;
    const detail = mappingDetails.get(speaker);
    return detail?.match_source === 'auto' || detail?.match_source === 'retroactive';
  }, [promotedSpeakers, mappingDetails]);

  // Categorize speakers into three groups
  const { autoAssigned, suggested, unassigned } = useMemo(() => {
    const auto: string[] = [];
    const suggest: string[] = [];
    const unmapped: string[] = [];

    for (const speaker of speakers) {
      const detail = mappingDetails.get(speaker);
      if (detail?.match_source === 'auto' || detail?.match_source === 'retroactive' || detail?.match_source === 'suggestion_promoted' || promotedSpeakers.has(speaker)) {
        auto.push(speaker);
      } else if (voiceSuggestions.has(speaker)) {
        suggest.push(speaker);
      } else {
        unmapped.push(speaker);
      }
    }

    return { autoAssigned: auto, suggested: suggest, unassigned: unmapped };
  }, [speakers, mappingDetails, voiceSuggestions, promotedSpeakers]);

  useEffect(() => {
    if (!open) {
      setActiveSpeaker(null);
      setHighlightedSuggestionIndex(0);
      setPromotedSpeakers(new Set());
      setMappingDetails(new Map());
      setRetranscribeOpen(false);
      setRetranscribeNumSpeakers("");
      setRetranscribeDiarizeMode("local");
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
              <div className="space-y-4 max-h-[400px] overflow-y-auto">
                {/* ── Auto-assigned speakers ── */}
                {autoAssigned.length > 0 && (
                  <div className="space-y-2">
                    <div className="flex items-center gap-2">
                      <div className="h-1.5 w-1.5 rounded-full bg-green-500" />
                      <span className="text-xs font-medium text-green-600 dark:text-green-400 uppercase tracking-wider">
                        Identified ({autoAssigned.length})
                      </span>
                    </div>
                    {autoAssigned.map((speaker) => {
                      const detail = mappingDetails.get(speaker);
                      return (
                        <div key={speaker} className="flex items-center gap-3 px-3 py-2 rounded-md bg-green-500/5 border border-green-500/20">
                          <div className="flex-1 min-w-0">
                            <div className="flex items-center gap-2">
                              <span className="text-xs text-muted-foreground">{formatSpeakerLabel(speaker)}</span>
                              <span className="text-xs px-1.5 py-0.5 rounded bg-green-500/10 text-green-600 dark:text-green-400 inline-flex items-center gap-1" data-testid={`badge-auto-${speaker}`}>
                                <Check className="h-3 w-3" />
                                {Math.round((detail?.confidence_score ?? 0) * 100)}%
                              </span>
                            </div>
                            <div className="text-sm font-medium truncate">{speakerMappings[speaker] || detail?.custom_name}</div>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                )}

                {/* ── Suggested speakers (pending review) ── */}
                {suggested.length > 0 && (
                  <div className="space-y-2">
                    <div className="flex items-center gap-2">
                      <div className="h-1.5 w-1.5 rounded-full bg-amber-500" />
                      <span className="text-xs font-medium text-amber-600 dark:text-amber-400 uppercase tracking-wider">
                        Suggestions ({suggested.length})
                      </span>
                    </div>
                    {suggested.map((speaker) => {
                      const suggestion = voiceSuggestions.get(speaker);
                      if (!suggestion) return null;
                      const isPromoting = promoteMutation.isPending && promoteMutation.variables?.originalSpeaker === speaker;
                      const isDismissing = dismissMutation.isPending && dismissMutation.variables?.mappingId === suggestion.id;

                      return (
                        <div key={speaker} className="px-3 py-2 rounded-md bg-amber-500/5 border border-amber-500/20" data-testid={`suggestion-${speaker}`}>
                          <div className="flex items-center gap-2 mb-1.5">
                            <span className="text-xs text-muted-foreground">{formatSpeakerLabel(speaker)}</span>
                            <span className="text-xs px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-600 dark:text-amber-400">
                              {Math.round((suggestion.confidence_score ?? 0) * 100)}% match
                            </span>
                          </div>
                          <div className="flex items-center gap-2">
                            <Sparkles className="h-4 w-4 text-amber-500 shrink-0" />
                            <span className="text-sm font-medium flex-1">
                              Is this <span className="text-amber-600 dark:text-amber-400">{suggestion.custom_name}</span>?
                            </span>
                            <div className="flex items-center gap-1">
                              <Button
                                variant="ghost"
                                size="sm"
                                className="h-7 px-2 text-green-600 hover:text-green-700 hover:bg-green-500/10"
                                disabled={isPromoting || isDismissing}
                                onClick={() => {
                                  promoteMutation.mutate(
                                    {
                                      transcriptionId,
                                      originalSpeaker: speaker,
                                      contactId: suggestion.contact_id!,
                                      contactName: suggestion.custom_name,
                                      score: suggestion.confidence_score ?? 0,
                                    },
                                    {
                                      onSuccess: (data) => {
                                        setSpeakerMappings((prev) => ({
                                          ...prev,
                                          [speaker]: suggestion.custom_name,
                                        }));
                                        setPromotedSpeakers((prev) => new Set(prev).add(speaker));
                                        onSpeakerMappingsUpdate(data.mappings);
                                        toast({
                                          title: `Speaker assigned to ${suggestion.custom_name}`,
                                          description: `Voice match with ${Math.round((suggestion.confidence_score ?? 0) * 100)}% confidence`,
                                        });
                                      },
                                    },
                                  );
                                }}
                              >
                                {isPromoting ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Check className="h-3.5 w-3.5" />}
                                <span className="ml-1">Yes</span>
                              </Button>
                              <Button
                                variant="ghost"
                                size="sm"
                                className="h-7 px-2 text-muted-foreground hover:text-red-600 hover:bg-red-500/10"
                                disabled={isPromoting || isDismissing}
                                onClick={() => {
                                  if (!suggestion.id) return;
                                  dismissMutation.mutate(
                                    { transcriptionId, mappingId: suggestion.id },
                                    {
                                      onSuccess: () => {
                                        toast({ title: `Suggestion dismissed for ${formatSpeakerLabel(speaker)}` });
                                      },
                                    },
                                  );
                                }}
                              >
                                {isDismissing ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <X className="h-3.5 w-3.5" />}
                                <span className="ml-1">No</span>
                              </Button>
                            </div>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                )}

                {/* ── Unassigned speakers ── */}
                {unassigned.length > 0 && (
                  <div className="space-y-2">
                    {(autoAssigned.length > 0 || suggested.length > 0) && (
                      <div className="flex items-center gap-2">
                        <div className="h-1.5 w-1.5 rounded-full bg-muted-foreground/40" />
                        <span className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
                          Unassigned ({unassigned.length})
                        </span>
                      </div>
                    )}
                    {unassigned.map((speaker) => {
                      const suggestions = getContactSuggestions(speakerMappings[speaker] ?? "", speaker);

                      return (
                        <div key={speaker} className="space-y-1">
                          <Label htmlFor={`speaker-${speaker}`} className="text-xs font-medium text-muted-foreground">
                            {formatSpeakerLabel(speaker)}
                          </Label>
                          <Popover
                            open={activeSpeaker === speaker}
                            onOpenChange={(nextOpen) => {
                              setActiveSpeaker((current) => {
                                if (nextOpen) return speaker;
                                return current === speaker ? null : current;
                              });
                              if (!nextOpen) setHighlightedSuggestionIndex(0);
                            }}
                          >
                            <PopoverAnchor asChild>
                              <Input
                                id={`speaker-${speaker}`}
                                value={speakerMappings[speaker] || ''}
                                onChange={(e) => handleSpeakerNameChange(speaker, e.target.value)}
                                onFocus={() => { setActiveSpeaker(speaker); setHighlightedSuggestionIndex(0); }}
                                onClick={() => { setActiveSpeaker(speaker); setHighlightedSuggestionIndex(0); }}
                                onKeyDown={(event) => handleSpeakerInputKeyDown(event, speaker)}
                                placeholder={`Enter custom name for ${formatSpeakerLabel(speaker)}`}
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
                                  <div className="px-3 py-2 text-xs text-[var(--text-tertiary)]">Loading contacts...</div>
                                ) : (
                                  <>
                                    {suggestions.map((contact, index) => (
                                      <button
                                        type="button"
                                        role="option"
                                        aria-selected={index === highlightedSuggestionIndex}
                                        key={contact.id}
                                        onMouseEnter={() => setHighlightedSuggestionIndex(index)}
                                        onMouseDown={(event) => { event.preventDefault(); applyContactSuggestion(speaker, contact); }}
                                        className={`w-full px-3 py-2 text-left transition-colors ${index === highlightedSuggestionIndex ? "bg-[var(--bg-muted-pane)]" : "hover:bg-[var(--bg-muted-pane)]/70"}`}
                                      >
                                        <div className="text-sm font-medium text-[var(--text-primary)]">{contact.name}</div>
                                        {(contact.email || contact.phone) && (
                                          <div className="text-xs text-[var(--text-secondary)]">{contact.email || contact.phone}</div>
                                        )}
                                      </button>
                                    ))}
                                    {isNewContactCandidate(speakerMappings[speaker] ?? "", speaker) && (() => {
                                      const createIndex = suggestions.length;
                                      const trimmedName = (speakerMappings[speaker] ?? "").trim();
                                      return (
                                        <>
                                          {suggestions.length > 0 && <div className="border-t border-[var(--border-subtle)] my-1" />}
                                          <button
                                            type="button"
                                            role="option"
                                            aria-selected={createIndex === highlightedSuggestionIndex}
                                            onMouseEnter={() => setHighlightedSuggestionIndex(createIndex)}
                                            onMouseDown={(event) => { event.preventDefault(); handleCreateContact(speaker, trimmedName); }}
                                            disabled={createContactMutation.isPending}
                                            className={`w-full px-3 py-2 text-left transition-colors flex items-center gap-2 ${createIndex === highlightedSuggestionIndex ? "bg-[var(--bg-muted-pane)]" : "hover:bg-[var(--bg-muted-pane)]/70"}`}
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
                                      <div className="px-3 py-2 text-xs text-[var(--text-tertiary)]">No contacts yet. Type a name to create one.</div>
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
          </div>
        )}

        {/* Re-transcribe Section */}
        {!isLoading && speakers.length > 0 && (
          <div className="border-t border-[var(--border-subtle)] pt-3">
            <button
              type="button"
              onClick={() => setRetranscribeOpen(!retranscribeOpen)}
              className="flex items-center gap-2 text-sm text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors w-full"
            >
              <RefreshCw className="h-3.5 w-3.5" />
              <span>Re-transcribe with different settings</span>
              <ChevronDown className={`h-3.5 w-3.5 ml-auto transition-transform ${retranscribeOpen ? 'rotate-180' : ''}`} />
            </button>

            {retranscribeOpen && (
              <div className="mt-3 space-y-3">
                <div className="space-y-1.5">
                  <Label className="text-xs text-[var(--text-tertiary)]">Model</Label>
                  <Select
                    value={retranscribeFamily}
                    onValueChange={(v) => {
                      setRetranscribeFamily(v);
                      const models = getModelsForFamily(v);
                      setRetranscribeModel(models[models.length - 1]);
                    }}
                  >
                    <SelectTrigger className="h-8 text-sm">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {MODEL_FAMILIES.map((f) => (
                        <SelectItem key={f.value} value={f.value}>{f.label}</SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>

                <div className="space-y-1.5">
                  <Label className="text-xs text-[var(--text-tertiary)]">Model Size</Label>
                  <Select value={retranscribeModel} onValueChange={setRetranscribeModel}>
                    <SelectTrigger className="h-8 text-sm">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {getModelsForFamily(retranscribeFamily).map((m) => (
                        <SelectItem key={m} value={m}>{m}</SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>

                <div className="space-y-1.5">
                  <Label className="text-xs text-[var(--text-tertiary)]">Speaker Diarization</Label>
                  <Select value={retranscribeDiarizeMode} onValueChange={(v) => setRetranscribeDiarizeMode(v as typeof retranscribeDiarizeMode)}>
                    <SelectTrigger className="h-8 text-sm">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="local">NVIDIA Sortformer</SelectItem>
                      <SelectItem value="pyannote">Pyannote</SelectItem>
                    </SelectContent>
                  </Select>
                  <p className="text-xs text-[var(--text-tertiary)]">
                    {retranscribeDiarizeMode === "local" && "NVIDIA Sortformer runs locally without a Hugging Face token."}
                    {retranscribeDiarizeMode === "pyannote" && "Pyannote provides high-accuracy diarization. Configure your Hugging Face token in Settings \u2192 Transcription."}
                  </p>
                </div>

                <div className="space-y-1.5">
                  <Label className="text-xs text-[var(--text-tertiary)]">
                    {retranscribeDiarizeMode === "local" ? "Max Speakers (optional)" : "Number of Speakers (optional)"}
                  </Label>
                  <Input
                    type="number"
                    min={1}
                    max={retranscribeDiarizeMode === "local" ? 8 : 20}
                    placeholder="Auto-detect"
                    value={retranscribeNumSpeakers}
                    onChange={(e) => setRetranscribeNumSpeakers(e.target.value)}
                    className="h-8 text-sm"
                  />
                  <p className="text-xs text-[var(--text-tertiary)]">
                    {retranscribeDiarizeMode === "local"
                      ? "Sortformer supports up to 8 speakers. Leave blank to auto-detect (defaults to 4)."
                      : "Set the exact number of speakers for more accurate diarization."
                    }
                  </p>
                </div>

                <Button
                  onClick={handleRetranscribe}
                  disabled={isRetranscribing}
                  variant="outline"
                  className="w-full"
                  size="sm"
                >
                  {isRetranscribing ? (
                    <>
                      <Loader2 className="h-4 w-4 mr-1.5 animate-spin" />
                      Starting...
                    </>
                  ) : (
                    <>
                      <RefreshCw className="h-4 w-4 mr-1.5" />
                      Re-transcribe
                    </>
                  )}
                </Button>
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
