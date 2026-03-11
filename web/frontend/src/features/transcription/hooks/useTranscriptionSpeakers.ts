import { useQuery } from "@tanstack/react-query";
import { useAuth } from "@/features/auth/hooks/useAuth";

export interface SpeakerMapping {
    id?: number;
    original_speaker: string;
    custom_name: string;
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

