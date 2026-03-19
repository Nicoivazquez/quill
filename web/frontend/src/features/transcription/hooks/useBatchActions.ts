import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useAuth } from '@/features/auth/hooks/useAuth';
import { FOLDERS_QUERY_KEY } from './useFolders';

interface BatchResult {
    id: string;
    success: boolean;
    error?: string;
}

interface BatchResponse {
    results: BatchResult[];
}

export function useBatchDelete() {
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async (ids: string[]) => {
            const response = await fetch('/api/v1/transcription/batch/delete', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', ...getAuthHeaders() },
                body: JSON.stringify({ ids }),
            });
            if (!response.ok) {
                const err = await response.json().catch(() => null);
                throw new Error(err?.error || 'Batch delete failed');
            }
            return response.json() as Promise<BatchResponse>;
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['audioFiles'] });
            queryClient.invalidateQueries({ queryKey: FOLDERS_QUERY_KEY });
        },
    });
}

export function useBatchMove() {
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async ({ ids, folder }: { ids: string[]; folder: string }) => {
            const response = await fetch('/api/v1/transcription/batch/move', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', ...getAuthHeaders() },
                body: JSON.stringify({ ids, folder }),
            });
            if (!response.ok) {
                const err = await response.json().catch(() => null);
                throw new Error(err?.error || 'Batch move failed');
            }
            return response.json() as Promise<BatchResponse>;
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['audioFiles'] });
            queryClient.invalidateQueries({ queryKey: FOLDERS_QUERY_KEY });
        },
    });
}

export function useBatchStart() {
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async ({ ids, params }: { ids: string[]; params: object }) => {
            const response = await fetch('/api/v1/transcription/batch/start', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', ...getAuthHeaders() },
                body: JSON.stringify({ ids, params }),
            });
            if (!response.ok) {
                const err = await response.json().catch(() => null);
                throw new Error(err?.error || 'Batch start failed');
            }
            return response.json() as Promise<BatchResponse>;
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['audioFiles'] });
        },
    });
}
