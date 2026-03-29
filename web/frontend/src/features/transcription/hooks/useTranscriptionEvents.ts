import { useEffect, useRef } from 'react';
import { useAuth } from '@/features/auth/hooks/useAuth';
import { useQueryClient } from '@tanstack/react-query';
import type { AudioFile } from '@/features/transcription/hooks/useAudioFiles';

interface JobUpdateEvent {
    type: string;
    payload: {
        job_id: string;
        status: string;
        error?: string;
        progress?: number;
    };
}

export interface SpeakerSuggestion {
    speaker: string;
    contact_id: number;
    contact_name: string;
    score: number;
    tier: 'auto' | 'suggest' | 'unknown';
}

export interface SpeakerIdentificationEvent {
    job_id: string;
    auto_assigned: SpeakerSuggestion[];
    suggestions: SpeakerSuggestion[];
    unmatched: string[];
}

export const useTranscriptionEvents = (jobId: string | null) => {
    const { token, isLocalMode, getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();
    const abortControllerRef = useRef<AbortController | null>(null);

    useEffect(() => {
        if (!jobId) return;
        if (!isLocalMode && !token) return;

        // Cleanup previous connection if any
        if (abortControllerRef.current) {
            abortControllerRef.current.abort();
        }

        const abortController = new AbortController();
        abortControllerRef.current = abortController;

        const connect = async () => {
            try {
                const response = await fetch(`/api/v1/events?job_id=${jobId}`, {
                    headers: getAuthHeaders(),
                    signal: abortController.signal,
                });

                if (!response.ok) {
                    throw new Error(`SSE connection failed: ${response.status}`);
                }

                if (!response.body) {
                    throw new Error('No response body');
                }

                const reader = response.body.getReader();
                const decoder = new TextDecoder();
                let buffer = '';

                while (true) {
                    const { done, value } = await reader.read();
                    if (done) break;

                    const chunk = decoder.decode(value, { stream: true });
                    buffer += chunk;

                    const lines = buffer.split('\n\n');
                    // Keep the last partial line in buffer
                    buffer = lines.pop() || '';

                    for (const line of lines) {
                        const trimmed = line.trim();
                        if (!trimmed || trimmed.startsWith(':')) continue; // Skip comments/keepalives

                        if (trimmed.startsWith('data: ')) {
                            const data = trimmed.slice(6);
                            try {
                                const event = JSON.parse(data);
                                handleEvent(event);
                            } catch (e) {
                                console.error('Failed to parse SSE data:', e);
                            }
                        }
                    }
                }
            } catch (error) {
                if ((error as Error).name !== 'AbortError') {
                    // Ignore "Error in input stream" which happens on abort/close in some browsers
                    const errorMsg = (error as Error).message;
                    if (!errorMsg.includes('Error in input stream')) {
                        console.error('SSE connection error, reconnecting in 5s...', error);
                        setTimeout(() => {
                            if (!abortController.signal.aborted) {
                                connect();
                            }
                        }, 5000);
                    }
                }
            }
        };

        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        const handleEvent = (event: any) => {
            if (event.type === 'job_update') {
                const payload = event.payload as JobUpdateEvent['payload'];

                // When a job completes, invalidate the transcript + detail queries
                // so the detail page picks up the new content automatically.
                if (payload.status === 'completed' || payload.status === 'failed') {
                    queryClient.invalidateQueries({ queryKey: ['transcript', payload.job_id] });
                    queryClient.invalidateQueries({ queryKey: ['audio', payload.job_id] });
                    queryClient.invalidateQueries({ queryKey: ['executionData', payload.job_id] });
                }

                // Optimistically update the list
                // eslint-disable-next-line @typescript-eslint/no-explicit-any
                queryClient.setQueriesData({ queryKey: ['audioFiles'] }, (oldData: any) => {
                    if (!oldData) return oldData;

                    // Handle generic infinite query structure
                    if (oldData.pages) {
                        return {
                            ...oldData,
                            // eslint-disable-next-line @typescript-eslint/no-explicit-any
                            pages: oldData.pages.map((page: any) => ({
                                ...page,
                                jobs: page.jobs.map((job: AudioFile) => {
                                    if (job.id === payload.job_id) {
                                        return {
                                            ...job,
                                            status: payload.status,
                                            error_message: payload.error || job.error_message,
                                        };
                                    }
                                    return job;
                                }),
                            })),
                        };
                    }

                    // Handle standard query structure (if used elsewhere)
                    if (oldData.jobs) {
                        return {
                            ...oldData,
                            jobs: oldData.jobs.map((job: AudioFile) => {
                                if (job.id === payload.job_id) {
                                    return {
                                        ...job,
                                        status: payload.status,
                                        error_message: payload.error || job.error_message,
                                    };
                                }
                                return job;
                            }),
                        };
                    }

                    return oldData;
                });
            }

            if (event.type === 'speaker_identification') {
                const payload = event.payload as SpeakerIdentificationEvent;

                // Suggestions and auto-assigned mappings are now persisted to the DB.
                // Invalidate the DB-backed queries so hooks refetch fresh data.
                queryClient.invalidateQueries({
                    queryKey: ['speakerSuggestions', payload.job_id],
                });
                queryClient.invalidateQueries({
                    queryKey: ['speakerMappings', payload.job_id],
                });
                queryClient.invalidateQueries({
                    queryKey: ['audioFiles'],
                });
            }
        };

        connect();

        return () => {
            abortController.abort();
        };
    }, [token, isLocalMode, getAuthHeaders, queryClient, jobId]);
};
