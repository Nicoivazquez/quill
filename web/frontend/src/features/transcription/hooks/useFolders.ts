import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useAuth } from '@/features/auth/hooks/useAuth';

export const FOLDERS_QUERY_KEY = ['folders'] as const;

export function useFolders() {
    const { getAuthHeaders } = useAuth();

    return useQuery({
        queryKey: FOLDERS_QUERY_KEY,
        staleTime: 30_000,
        queryFn: async () => {
            const response = await fetch('/api/v1/transcription/folders', {
                headers: getAuthHeaders(),
            });
            if (!response.ok) throw new Error('Failed to fetch folders');
            const data = await response.json();
            return (data.folders || []) as string[];
        },
    });
}

export function useCreateFolder() {
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async (name: string) => {
            const response = await fetch('/api/v1/transcription/folders', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', ...getAuthHeaders() },
                body: JSON.stringify({ name }),
            });
            if (!response.ok) {
                const err = await response.json();
                throw new Error(err.error || 'Failed to create folder');
            }
            return response.json();
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: FOLDERS_QUERY_KEY });
        },
    });
}

export function useRenameFolder() {
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async ({ oldName, newName }: { oldName: string; newName: string }) => {
            const response = await fetch('/api/v1/transcription/folders/rename', {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json', ...getAuthHeaders() },
                body: JSON.stringify({ old_name: oldName, new_name: newName }),
            });
            if (!response.ok) {
                const err = await response.json();
                throw new Error(err.error || 'Failed to rename folder');
            }
            return response.json();
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: FOLDERS_QUERY_KEY });
            queryClient.invalidateQueries({ queryKey: ['audioFiles'] });
        },
    });
}

export function useDeleteFolder() {
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async (name: string) => {
            const response = await fetch(`/api/v1/transcription/folders?name=${encodeURIComponent(name)}`, {
                method: 'DELETE',
                headers: getAuthHeaders(),
            });
            if (!response.ok) {
                const err = await response.json();
                throw new Error(err.error || 'Failed to delete folder');
            }
            return response.json();
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: FOLDERS_QUERY_KEY });
            queryClient.invalidateQueries({ queryKey: ['audioFiles'] });
        },
    });
}

export function useMoveToFolder() {
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async ({ jobId, folder }: { jobId: string; folder: string }) => {
            const response = await fetch(`/api/v1/transcription/${jobId}/folder`, {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json', ...getAuthHeaders() },
                body: JSON.stringify({ folder }),
            });
            if (!response.ok) {
                const err = await response.json();
                throw new Error(err.error || 'Failed to move to folder');
            }
            return response.json();
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: FOLDERS_QUERY_KEY });
            queryClient.invalidateQueries({ queryKey: ['audioFiles'] });
        },
    });
}
