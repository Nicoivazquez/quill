import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@/features/auth/hooks/useAuth";
import type { SpeakerIdentificationEvent } from "@/features/transcription/hooks/useTranscriptionEvents";

export interface SpeakerMapping {
    id?: number;
    original_speaker: string;
    custom_name: string;
    confidence_score?: number;
    match_source?: string;  // "auto" | "manual" | "suggestion_promoted"
    match_tier?: string;    // "auto" | "suggest" | "unknown" | ""
}

export interface SpeakerContactBootstrapSummary {
    started_count: number;
    created_count: number;
    skipped_existing_count: number;
}

export interface SpeakerMappingsUpdateResponse {
    mappings: SpeakerMapping[];
    contact_bootstrap: SpeakerContactBootstrapSummary;
}

export function useSpeakerMappings(audioId: string, enabled: boolean) {
    const { getAuthHeaders } = useAuth();

    return useQuery({
        queryKey: ["speakerMappings", audioId],
        queryFn: async () => {
            const response = await fetch(`/api/v1/transcription/${audioId}/speakers`, {
                headers: getAuthHeaders(),
            });
            if (!response.ok) throw new Error("Failed to fetch speaker mappings");
            const mappings: SpeakerMapping[] = await response.json();

            // Convert to lookup object for easier consumption
            const mappingObj: Record<string, string> = {};
            mappings.forEach(mapping => {
                mappingObj[mapping.original_speaker] = mapping.custom_name;
            });
            return mappingObj;
        },
        enabled: enabled,
    });
}

export interface PromoteSuggestionParams {
    transcriptionId: string;
    originalSpeaker: string;
    contactId: number;
    contactName: string;
    score: number;
}

export function usePromoteSpeakerSuggestion() {
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async (params: PromoteSuggestionParams) => {
            const response = await fetch(
                `/api/v1/transcription/${params.transcriptionId}/speakers/promote`,
                {
                    method: "POST",
                    headers: { "Content-Type": "application/json", ...getAuthHeaders() },
                    body: JSON.stringify({
                        original_speaker: params.originalSpeaker,
                        contact_id: params.contactId,
                        contact_name: params.contactName,
                        score: params.score,
                    }),
                },
            );
            if (!response.ok) throw new Error("Failed to promote suggestion");
            return response.json() as Promise<SpeakerMappingsUpdateResponse>;
        },
        onSuccess: (_data, params) => {
            // Remove promoted speaker from suggestions cache
            queryClient.setQueryData<SpeakerIdentificationEvent>(
                ["speakerSuggestions", params.transcriptionId],
                (old) => {
                    if (!old) return old;
                    return {
                        ...old,
                        suggestions: old.suggestions.filter(
                            (s) => s.speaker !== params.originalSpeaker,
                        ),
                    };
                },
            );

            // Invalidate speaker mappings so the dialog re-fetches
            queryClient.invalidateQueries({
                queryKey: ["speakerMappings", params.transcriptionId],
            });

            // Invalidate audioFiles so the list reflects updated speaker names
            queryClient.invalidateQueries({
                queryKey: ["audioFiles"],
            });
        },
    });
}

