import { useQuery } from '@tanstack/react-query';
import { useAuth } from '@/features/auth/hooks/useAuth';

export function useSpeakers() {
    const { getAuthHeaders } = useAuth();

    return useQuery({
        queryKey: ['speakers', 'distinct'],
        queryFn: async () => {
            const response = await fetch('/api/v1/transcription/speakers/distinct', {
                headers: getAuthHeaders(),
            });

            if (!response.ok) {
                throw new Error('Failed to fetch speakers');
            }

            const data = await response.json();
            return (data.speakers || []) as string[];
        },
        staleTime: 30_000,
    });
}
