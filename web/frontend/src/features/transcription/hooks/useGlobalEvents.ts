import { useEffect, useRef } from 'react';
import { useAuth } from '@/features/auth/hooks/useAuth';
import { useQueryClient } from '@tanstack/react-query';
import { useToast } from '@/components/ui/toast';

/**
 * useGlobalEvents subscribes to the global SSE endpoint (/api/v1/events/global)
 * for list-level events like speaker_attention_updated. Unlike useTranscriptionEvents,
 * this does not require a job_id — it receives all BroadcastGlobal events.
 */
export const useGlobalEvents = () => {
    const { token, isLocalMode, getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();
    const { toast } = useToast();
    const abortControllerRef = useRef<AbortController | null>(null);

    useEffect(() => {
        if (!isLocalMode && !token) return;

        if (abortControllerRef.current) {
            abortControllerRef.current.abort();
        }

        const abortController = new AbortController();
        abortControllerRef.current = abortController;

        const connect = async () => {
            try {
                const response = await fetch('/api/v1/events/global', {
                    headers: getAuthHeaders(),
                    signal: abortController.signal,
                });

                if (!response.ok) {
                    throw new Error(`Global SSE connection failed: ${response.status}`);
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
                    buffer = lines.pop() || '';

                    for (const line of lines) {
                        const trimmed = line.trim();
                        if (!trimmed || trimmed.startsWith(':')) continue;

                        if (trimmed.startsWith('data: ')) {
                            const data = trimmed.slice(6);
                            try {
                                const event = JSON.parse(data);
                                handleEvent(event);
                            } catch (e) {
                                console.error('Failed to parse global SSE data:', e);
                            }
                        }
                    }
                }
            } catch (error) {
                if ((error as Error).name !== 'AbortError') {
                    const errorMsg = (error as Error).message;
                    if (!errorMsg.includes('Error in input stream')) {
                        console.error('Global SSE connection error, reconnecting in 5s...', error);
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
            if (event.type === 'speaker_attention_updated' || event.type === 'title_updated') {
                // A speaker identification pipeline or auto-title completed for some job.
                // Invalidate the list so the updated data refreshes.
                queryClient.invalidateQueries({ queryKey: ['audioFiles'] });
            } else if (event.type === 'notification') {
                const payload = event.payload;
                if (payload) {
                    const variant = payload.level === 'error' ? 'error' as const
                        : payload.level === 'warning' ? 'warning' as const
                        : 'default' as const;
                    toast({
                        title: payload.message || 'System notification',
                        variant,
                    });
                }
            }
        };

        connect();

        return () => {
            abortController.abort();
        };
    }, [token, isLocalMode, getAuthHeaders, queryClient, toast]);
};
