import { useQuery, useMutation, useQueryClient, useInfiniteQuery } from '@tanstack/react-query';
import { useAuth } from '@/features/auth/hooks/useAuth';

export class DuplicateUploadError extends Error {
    existingId: string;
    existingTitle: string;
    file: File;
    isVideo: boolean;
    title?: string;

    constructor(existingId: string, existingTitle: string, file: File, isVideo: boolean, title?: string) {
        super(`This file has already been uploaded as "${existingTitle || 'Untitled'}"`);
        this.name = 'DuplicateUploadError';
        this.existingId = existingId;
        this.existingTitle = existingTitle;
        this.file = file;
        this.isVideo = isVideo;
        this.title = title;
    }
}

export interface AudioFile {
    id: string;
    title?: string;
    status: "uploaded" | "pending" | "processing" | "completed" | "failed";
    created_at: string;
    audio_path: string;
    diarization?: boolean;
    is_multi_track?: boolean;
    error_message?: string;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    individual_transcripts?: any;
    speakers?: number;
    duration?: number;
    folder?: string;
    updated_at?: string;
    obsidian_synced_at?: string;
}

export interface SpeakerAttentionSummary {
    pending_suggestions: number;
    auto_assigned: number;
    total_mappings: number;
    renamed: number;
}

export interface AudioFilesResponse {
    jobs: AudioFile[];
    pending_suggestions?: Record<string, number>;
    speaker_attention?: Record<string, SpeakerAttentionSummary>;
    pagination: {
        page: number;
        limit: number;
        total: number;
        pages: number;
    };
}

interface AudioListParams {
    page: number;
    limit: number;
    search?: string;
    sortBy?: string;
    sortOrder?: 'asc' | 'desc';
    folder?: string | null; // null = all, "" = root only, "Work" = specific folder
    status?: string; // filter by job status (e.g., "completed", "failed")
    speaker?: string; // filter by speaker custom name
    speakerStatus?: string; // filter by speaker attention ("needs_attention", "identified")
}

function getListRefetchInterval(data: AudioFilesResponse | undefined) {
    if (!data) {
        return 5000;
    }

    const hasActiveJobs = data.jobs.some(
        (job) => job.status === "pending" || job.status === "processing"
    );

    return hasActiveJobs ? 3000 : 5000;
}

export function useAudioListInfinite(params: Omit<AudioListParams, 'page'>) {
    const { getAuthHeaders } = useAuth();

    return useInfiniteQuery({
        queryKey: ['audioFiles', 'infinite', params],
        queryFn: async ({ pageParam = 1 }) => {
            const searchParams = new URLSearchParams({
                page: pageParam.toString(),
                limit: params.limit.toString(),
            });

            if (params.search) searchParams.set('q', params.search);
            if (params.sortBy) {
                searchParams.set('sort_by', params.sortBy);
                searchParams.set('sort_order', params.sortOrder || 'desc');
            }
            if (params.folder !== undefined && params.folder !== null) {
                searchParams.set('folder', params.folder);
            }
            if (params.status) searchParams.set('status', params.status);
            if (params.speaker) searchParams.set('speaker', params.speaker);
            if (params.speakerStatus) searchParams.set('speaker_status', params.speakerStatus);

            const response = await fetch(`/api/v1/transcription/list?${searchParams}`, {
                headers: getAuthHeaders(),
            });

            if (!response.ok) {
                throw new Error('Failed to fetch audio files');
            }

            return response.json() as Promise<AudioFilesResponse>;
        },
        getNextPageParam: (lastPage) => {
            if (lastPage.pagination.page < lastPage.pagination.pages) {
                return lastPage.pagination.page + 1;
            }
            return undefined;
        },
        initialPageParam: 1,
        refetchInterval: (query) => {
            const latestPage = query.state.data?.pages?.[0];
            return getListRefetchInterval(latestPage);
        }
    });
}

export function useAudioUpload() {
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async ({ file, isVideo, title, force }: { file: File, isVideo: boolean, title?: string, force?: boolean }) => {
            const formData = new FormData();
            const fieldName = isVideo ? 'video' : 'audio';
            let endpoint = isVideo ? '/api/v1/transcription/upload-video' : '/api/v1/transcription/upload';

            if (force) {
                endpoint += '?force=true';
            }

            formData.append(fieldName, file);
            const trimmedTitle = typeof title === "string" ? title.trim() : undefined;

            // Backward compatibility: when caller does not provide a title,
            // preserve existing filename-based title behavior.
            if (trimmedTitle === undefined) {
                formData.append('title', file.name.replace(/\.[^/.]+$/, ''));
            } else if (trimmedTitle !== "") {
                // Explicit title provided by caller.
                formData.append('title', trimmedTitle);
            }

            const response = await fetch(endpoint, {
                method: 'POST',
                headers: getAuthHeaders(),
                body: formData,
            });

            if (response.status === 409) {
                const data = await response.json();
                throw new DuplicateUploadError(
                    data.existing_id,
                    data.existing_title,
                    file,
                    isVideo,
                    title,
                );
            }

            if (!response.ok) {
                throw new Error('Upload failed');
            }
            return response.json();
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['audioFiles'] });
        },
    });
}

export function useMultiTrackUpload() {
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async ({ files, aupFile, title }: { files: File[], aupFile: File, title: string }) => {
            const formData = new FormData();
            formData.append('title', title);
            formData.append('aup', aupFile);

            files.forEach(file => {
                formData.append('tracks', file);
            });

            const response = await fetch("/api/v1/transcription/upload-multitrack", {
                method: "POST",
                headers: getAuthHeaders(),
                body: formData,
            });

            if (!response.ok) {
                const errorData = await response.json();
                throw new Error(errorData.error || 'Upload failed');
            }
            return response.json();
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['audioFiles'] });
        }
    });
}

export function useYouTubeDownload() {
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async ({ url, title }: { url: string, title?: string }) => {
            const response = await fetch("/api/v1/transcription/youtube", {
                method: "POST",
                headers: {
                    "Content-Type": "application/json",
                    ...getAuthHeaders(),
                },
                body: JSON.stringify({
                    url: url.trim(),
                    title: title?.trim() || undefined,
                }),
            });

            if (!response.ok) {
                const errorData = await response.json();
                throw new Error(errorData.error || "Failed to download YouTube audio");
            }
            return response.json();
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['audioFiles'] });
        },
    });
}

export interface Profile {
    id: string;
    name: string;
    description?: string;
    is_default: boolean;
}

export function useTranscriptionProfiles() {
    const { getAuthHeaders } = useAuth();
    return useQuery({
        queryKey: ['transcriptionProfiles'],
        queryFn: async () => {
            const response = await fetch("/api/v1/profiles/", {
                headers: getAuthHeaders(),
            });
            if (!response.ok) throw new Error('Failed to load profiles');
            return response.json() as Promise<Profile[]>;
        }
    });
}

export function useQuickTranscription() {
    const { getAuthHeaders } = useAuth();
    return useMutation({
        mutationFn: async ({ file, profileName }: { file: File, profileName?: string }) => {
            const formData = new FormData();
            formData.append("audio", file);
            if (profileName) formData.append("profile_name", profileName);

            const response = await fetch("/api/v1/transcription/quick", {
                method: "POST",
                headers: getAuthHeaders(),
                body: formData,
            });

            if (!response.ok) {
                const errorData = await response.json();
                throw new Error(errorData.error || "Failed to submit transcription");
            }
            return response.json();
        }
    });
}
