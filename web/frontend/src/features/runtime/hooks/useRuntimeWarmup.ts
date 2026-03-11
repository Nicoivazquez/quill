import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@/features/auth/hooks/useAuth";
import type { RuntimeWarmupStatus } from "@/features/runtime/types";

const QUERY_KEY = ["runtime-warmup"];

export function useRuntimeWarmup() {
  const { getAuthHeaders, isSetupCompleted } = useAuth();

  return useQuery<RuntimeWarmupStatus>({
    queryKey: QUERY_KEY,
    enabled: isSetupCompleted,
    queryFn: async () => {
      const response = await fetch("/api/v1/runtime/warmup", {
        headers: getAuthHeaders(),
      });

      if (!response.ok) {
        throw new Error(`Failed to load runtime warmup status: ${response.status}`);
      }

      return response.json();
    },
    staleTime: 2000,
    refetchInterval: (query) => {
      const status = query.state.data;
      if (!status?.enabled) {
        return false;
      }
      if (status.state === "running") {
        return 1500;
      }
      if (status.state === "failed") {
        return 10000;
      }
      return false;
    },
  });
}

export function useRetryRuntimeWarmup() {
  const queryClient = useQueryClient();
  const { getAuthHeaders } = useAuth();

  return useMutation({
    mutationFn: async () => {
      const response = await fetch("/api/v1/runtime/warmup/retry", {
        method: "POST",
        headers: getAuthHeaders(),
      });

      if (!response.ok) {
        throw new Error(`Failed to retry runtime warmup: ${response.status}`);
      }

      return response.json();
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: QUERY_KEY });
    },
  });
}
