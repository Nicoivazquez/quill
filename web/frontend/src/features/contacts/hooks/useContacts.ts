import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@/features/auth/hooks/useAuth";
import type { Contact, ContactAppearancesResponse, ContactFilesResponse, ContactRequest, ContactsListResponse } from "@/features/contacts/types";

const contactsKeys = {
  all: ["contacts"] as const,
  list: (query: string) => ["contacts", "list", query] as const,
  detail: (id: number) => ["contacts", "detail", id] as const,
  files: (id: number) => ["contacts", "files", id] as const,
  appearances: (id: number) => ["contacts", "appearances", id] as const,
};

async function parseError(response: Response, fallback: string): Promise<never> {
  let message = fallback;
  try {
    const payload = await response.json();
    if (typeof payload?.error === "string" && payload.error.trim()) {
      message = payload.error;
    }
  } catch {
    // Ignore JSON parse failures and keep fallback message.
  }
  throw new Error(message);
}

export function useContacts(query: string, enabled = true) {
  const { getAuthHeaders } = useAuth();

  return useQuery({
    enabled,
    queryKey: contactsKeys.list(query),
    queryFn: async () => {
      const params = new URLSearchParams();
      if (query.trim()) {
        params.set("q", query.trim());
      }
      const suffix = params.toString();
      const response = await fetch(`/api/v1/contacts${suffix ? `?${suffix}` : ""}`, {
        headers: getAuthHeaders(),
      });
      if (!response.ok) {
        return parseError(response, "Failed to fetch contacts");
      }
      return (await response.json()) as ContactsListResponse;
    },
  });
}

export function useContact(contactID: number | null) {
  const { getAuthHeaders } = useAuth();

  return useQuery({
    enabled: !!contactID,
    queryKey: contactID ? contactsKeys.detail(contactID) : ["contacts", "detail", "none"],
    queryFn: async () => {
      const response = await fetch(`/api/v1/contacts/${contactID}`, {
        headers: getAuthHeaders(),
      });
      if (!response.ok) {
        return parseError(response, "Failed to fetch contact");
      }
      return (await response.json()) as Contact;
    },
    refetchInterval: (query) => {
      const contact = query.state.data as Contact | undefined;
      return contact?.signature_status === "processing" ? 2000 : false;
    },
  });
}

export function useContactFiles(contactID: number | null, enabled: boolean) {
  const { getAuthHeaders } = useAuth();

  return useQuery({
    enabled: enabled && !!contactID,
    queryKey: contactID ? contactsKeys.files(contactID) : ["contacts", "files", "none"],
    queryFn: async () => {
      const response = await fetch(`/api/v1/contacts/${contactID}/files`, {
        headers: getAuthHeaders(),
      });
      if (!response.ok) {
        return parseError(response, "Failed to fetch contact files");
      }
      return (await response.json()) as ContactFilesResponse;
    },
  });
}

export function useContactAppearances(contactID: number | null) {
  const { getAuthHeaders } = useAuth();

  return useQuery({
    enabled: !!contactID,
    queryKey: contactID ? contactsKeys.appearances(contactID) : ["contacts", "appearances", "none"],
    queryFn: async () => {
      const response = await fetch(`/api/v1/contacts/${contactID}/appearances`, {
        headers: getAuthHeaders(),
      });
      if (!response.ok) {
        return parseError(response, "Failed to fetch appearances");
      }
      return (await response.json()) as ContactAppearancesResponse;
    },
  });
}

export function useCreateContact() {
  const { getAuthHeaders } = useAuth();
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (payload: ContactRequest) => {
      const response = await fetch("/api/v1/contacts", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...getAuthHeaders(),
        },
        body: JSON.stringify(payload),
      });
      if (!response.ok) {
        return parseError(response, "Failed to create contact");
      }
      return (await response.json()) as Contact;
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: contactsKeys.all });
    },
  });
}

export function useUpdateContact(contactID: number | null) {
  const { getAuthHeaders } = useAuth();
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (payload: ContactRequest) => {
      if (!contactID) {
        throw new Error("No contact selected");
      }
      const response = await fetch(`/api/v1/contacts/${contactID}`, {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
          ...getAuthHeaders(),
        },
        body: JSON.stringify(payload),
      });
      if (!response.ok) {
        return parseError(response, "Failed to update contact");
      }
      return (await response.json()) as Contact;
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: contactsKeys.all });
      if (contactID) {
        queryClient.invalidateQueries({ queryKey: contactsKeys.detail(contactID) });
        queryClient.invalidateQueries({ queryKey: contactsKeys.files(contactID) });
      }
    },
  });
}

export function useDeleteContact() {
  const { getAuthHeaders } = useAuth();
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (contactID: number) => {
      const response = await fetch(`/api/v1/contacts/${contactID}`, {
        method: "DELETE",
        headers: getAuthHeaders(),
      });
      if (!response.ok) {
        return parseError(response, "Failed to delete contact");
      }
      return true;
    },
    onSuccess: (_, contactID) => {
      queryClient.invalidateQueries({ queryKey: contactsKeys.all });
      queryClient.removeQueries({ queryKey: contactsKeys.detail(contactID) });
      queryClient.removeQueries({ queryKey: contactsKeys.files(contactID) });
    },
  });
}

export function useReindexContacts() {
  const { getAuthHeaders } = useAuth();
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async () => {
      const response = await fetch("/api/v1/contacts/reindex", {
        method: "POST",
        headers: getAuthHeaders(),
      });
      if (!response.ok) {
        return parseError(response, "Failed to reindex contacts");
      }
      return response.json();
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: contactsKeys.all });
    },
  });
}

export function useUploadSnippet(contactID: number | null) {
  const { getAuthHeaders } = useAuth();
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (file: File) => {
      if (!contactID) {
        throw new Error("No contact selected");
      }
      const formData = new FormData();
      formData.append("snippet", file);

      const response = await fetch(`/api/v1/contacts/${contactID}/snippet`, {
        method: "POST",
        headers: getAuthHeaders(),
        body: formData,
      });
      if (!response.ok) {
        return parseError(response, "Failed to upload snippet");
      }
      return response.json();
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: contactsKeys.all });
      if (contactID) {
        queryClient.invalidateQueries({ queryKey: contactsKeys.detail(contactID) });
        queryClient.invalidateQueries({ queryKey: contactsKeys.files(contactID) });
      }
    },
  });
}

export function useDeleteSnippet(contactID: number | null) {
  const { getAuthHeaders } = useAuth();
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async () => {
      if (!contactID) {
        throw new Error("No contact selected");
      }
      const response = await fetch(`/api/v1/contacts/${contactID}/snippet`, {
        method: "DELETE",
        headers: getAuthHeaders(),
      });
      if (!response.ok) {
        return parseError(response, "Failed to delete snippet");
      }
      return true;
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: contactsKeys.all });
      if (contactID) {
        queryClient.invalidateQueries({ queryKey: contactsKeys.detail(contactID) });
        queryClient.invalidateQueries({ queryKey: contactsKeys.files(contactID) });
      }
    },
  });
}

export function useUploadSignature(contactID: number | null) {
  const { getAuthHeaders } = useAuth();
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (file: File) => {
      if (!contactID) {
        throw new Error("No contact selected");
      }
      const formData = new FormData();
      formData.append("signature", file);

      const response = await fetch(`/api/v1/contacts/${contactID}/signature`, {
        method: "POST",
        headers: getAuthHeaders(),
        body: formData,
      });
      if (!response.ok) {
        return parseError(response, "Failed to upload signature");
      }
      return response.json();
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: contactsKeys.all });
      if (contactID) {
        queryClient.invalidateQueries({ queryKey: contactsKeys.detail(contactID) });
        queryClient.invalidateQueries({ queryKey: contactsKeys.files(contactID) });
      }
    },
  });
}

export function useDeleteSignature(contactID: number | null) {
  const { getAuthHeaders } = useAuth();
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async () => {
      if (!contactID) {
        throw new Error("No contact selected");
      }
      const response = await fetch(`/api/v1/contacts/${contactID}/signature`, {
        method: "DELETE",
        headers: getAuthHeaders(),
      });
      if (!response.ok) {
        return parseError(response, "Failed to clear signature");
      }
      return true;
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: contactsKeys.all });
      if (contactID) {
        queryClient.invalidateQueries({ queryKey: contactsKeys.detail(contactID) });
        queryClient.invalidateQueries({ queryKey: contactsKeys.files(contactID) });
      }
    },
  });
}

export function useRescanContact(contactID: number | null) {
  const { getAuthHeaders } = useAuth();
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async () => {
      if (!contactID) {
        throw new Error("No contact selected");
      }
      const response = await fetch(`/api/v1/contacts/${contactID}/rescan`, {
        method: "POST",
        headers: getAuthHeaders(),
      });
      if (!response.ok) {
        return parseError(response, "Failed to start retroactive scan");
      }
      return response.json();
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: contactsKeys.all });
      if (contactID) {
        queryClient.invalidateQueries({ queryKey: contactsKeys.detail(contactID) });
      }
    },
  });
}

export function usePullSnippet(contactID: number | null) {
  const { getAuthHeaders } = useAuth();
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async () => {
      if (!contactID) {
        throw new Error("No contact selected");
      }
      const response = await fetch(`/api/v1/contacts/${contactID}/snippet/pull`, {
        method: "POST",
        headers: getAuthHeaders(),
      });
      if (!response.ok) {
        return parseError(response, "Failed to pull voice snippet");
      }
      return response.json();
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: contactsKeys.all });
      if (contactID) {
        queryClient.invalidateQueries({ queryKey: contactsKeys.detail(contactID) });
        queryClient.invalidateQueries({ queryKey: contactsKeys.files(contactID) });
      }
    },
  });
}

export function useExtractSignature(contactID: number | null) {
  const { getAuthHeaders } = useAuth();
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async () => {
      if (!contactID) {
        throw new Error("No contact selected");
      }
      const response = await fetch(`/api/v1/contacts/${contactID}/signature/extract`, {
        method: "POST",
        headers: getAuthHeaders(),
      });
      if (!response.ok) {
        return parseError(response, "Failed to start signature extraction");
      }
      return response.json();
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: contactsKeys.all });
      if (contactID) {
        queryClient.invalidateQueries({ queryKey: contactsKeys.detail(contactID) });
        queryClient.invalidateQueries({ queryKey: contactsKeys.files(contactID) });
      }
    },
  });
}
