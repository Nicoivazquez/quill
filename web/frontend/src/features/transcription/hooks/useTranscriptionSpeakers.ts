import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@/features/auth/hooks/useAuth";

export interface SpeakerMapping {
    id?: number;
    original_speaker: string;
    custom_name: string;
    contact_id?: number;
    confidence_score?: number;
    match_source?: string;  // "auto" | "manual" | "suggestion_promoted" | "retroactive"
    match_tier?: string;    // "auto" | "suggest" | "unknown" | ""
    review_status?: string; // "" | "pending" | "accepted" | "dismissed"
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

            // Convert to lookup object for easier consumption.
            // Skip mappings where custom_name equals the raw label (no real rename)
            // so the UI falls through to the friendly "Speaker A/B/C" format.
            const mappingObj: Record<string, string> = {};
            mappings.forEach(mapping => {
                if (mapping.custom_name && mapping.custom_name !== mapping.original_speaker) {
                    mappingObj[mapping.original_speaker] = mapping.custom_name;
                }
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

export function useSpeakerSuggestions(transcriptionId: string, enabled: boolean) {
    const { getAuthHeaders } = useAuth();

    return useQuery({
        queryKey: ["speakerSuggestions", transcriptionId],
        queryFn: async () => {
            const response = await fetch(
                `/api/v1/transcription/${transcriptionId}/speakers/suggestions`,
                { headers: getAuthHeaders() },
            );
            if (!response.ok) throw new Error("Failed to fetch speaker suggestions");
            return response.json() as Promise<SpeakerMapping[]>;
        },
        enabled: enabled && !!transcriptionId,
    });
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
            // Invalidate suggestions so they re-fetch from DB
            queryClient.invalidateQueries({
                queryKey: ["speakerSuggestions", params.transcriptionId],
            });

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

export function useDismissSpeakerSuggestion() {
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async (params: { transcriptionId: string; mappingId: number }) => {
            const response = await fetch(
                `/api/v1/transcription/${params.transcriptionId}/speakers/dismiss`,
                {
                    method: "POST",
                    headers: { "Content-Type": "application/json", ...getAuthHeaders() },
                    body: JSON.stringify({ mapping_id: params.mappingId }),
                },
            );
            if (!response.ok) throw new Error("Failed to dismiss suggestion");
            return response.json();
        },
        onSuccess: (_data, params) => {
            // Invalidate suggestions so they re-fetch from DB
            queryClient.invalidateQueries({
                queryKey: ["speakerSuggestions", params.transcriptionId],
            });

            // Invalidate audioFiles so badge counts update
            queryClient.invalidateQueries({
                queryKey: ["audioFiles"],
            });
        },
    });
}

