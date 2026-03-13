import { useState, useEffect, useCallback } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Cloud, Key, CheckCircle, AlertCircle, Trash2, Loader2 } from "lucide-react";
import { useAuth } from "@/features/auth/hooks/useAuth";

interface CloudProvider {
  provider: string;
  has_key: boolean;
  is_active: boolean;
}

const PROVIDER_INFO: Record<string, { label: string; description: string; placeholder: string }> = {
  assemblyai: {
    label: "AssemblyAI",
    description: "Cloud transcription + diarization (up to 50 speakers).",
    placeholder: "Enter AssemblyAI API key",
  },
  deepgram: {
    label: "Deepgram",
    description: "Nova-3 cloud transcription + diarization.",
    placeholder: "Enter Deepgram API key",
  },
  openai: {
    label: "OpenAI",
    description: "Whisper transcription + LLM features. Key is synced with LLM settings.",
    placeholder: "sk-...",
  },
};

export function CloudProviderSettings() {
  const [providers, setProviders] = useState<CloudProvider[]>([]);
  const [apiKeys, setApiKeys] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(true);
  const [savingProvider, setSavingProvider] = useState<string | null>(null);
  const [deletingProvider, setDeletingProvider] = useState<string | null>(null);
  const [message, setMessage] = useState<{ type: "success" | "error"; text: string } | null>(null);
  const { getAuthHeaders } = useAuth();

  const fetchProviders = useCallback(async () => {
    try {
      const response = await fetch("/api/v1/cloud-providers", {
        headers: getAuthHeaders(),
      });
      if (response.ok) {
        const data: CloudProvider[] = await response.json();
        setProviders(data);
      }
    } catch {
      // Silently handle fetch errors — the component renders an empty state gracefully.
    } finally {
      setLoading(false);
    }
  }, [getAuthHeaders]);

  useEffect(() => {
    fetchProviders();
  }, [fetchProviders]);

  const handleSave = async (provider: string) => {
    const key = apiKeys[provider]?.trim();
    if (!key) return;

    setSavingProvider(provider);
    setMessage(null);

    try {
      const response = await fetch(`/api/v1/cloud-providers/${provider}`, {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
          ...getAuthHeaders(),
        },
        body: JSON.stringify({ api_key: key }),
      });

      if (response.ok) {
        const updated: CloudProvider = await response.json();
        setProviders((prev) =>
          prev.map((p) => (p.provider === provider ? updated : p))
        );
        setApiKeys((prev) => ({ ...prev, [provider]: "" }));
        const info = PROVIDER_INFO[provider];
        const extra = provider === "openai" ? " Key also synced to LLM config." : "";
        setMessage({ type: "success", text: `${info?.label ?? provider} API key saved.${extra}` });
      } else {
        const err = await response.json().catch(() => ({}));
        setMessage({ type: "error", text: (err as { error?: string }).error || "Failed to save API key" });
      }
    } catch {
      setMessage({ type: "error", text: "Failed to save API key. Please try again." });
    } finally {
      setSavingProvider(null);
    }
  };

  const handleDelete = async (provider: string) => {
    setDeletingProvider(provider);
    setMessage(null);

    try {
      const response = await fetch(`/api/v1/cloud-providers/${provider}`, {
        method: "DELETE",
        headers: getAuthHeaders(),
      });

      if (response.ok || response.status === 204) {
        setProviders((prev) =>
          prev.map((p) =>
            p.provider === provider ? { ...p, has_key: false, is_active: false } : p
          )
        );
        const info = PROVIDER_INFO[provider];
        setMessage({ type: "success", text: `${info?.label ?? provider} API key removed.` });
      } else {
        setMessage({ type: "error", text: "Failed to remove API key" });
      }
    } catch {
      setMessage({ type: "error", text: "Failed to remove API key. Please try again." });
    } finally {
      setDeletingProvider(null);
    }
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center h-32">
        <div className="text-[var(--text-tertiary)]">Loading cloud provider settings...</div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="bg-[var(--bg-main)]/50 border border-[var(--border-subtle)] rounded-[var(--radius-card)] p-4 sm:p-6 shadow-sm">
        <div className="mb-4 sm:mb-6">
          <h3 className="text-lg font-medium text-[var(--text-primary)] flex items-center gap-2">
            <Cloud className="h-5 w-5 text-[var(--brand-solid)]" />
            Cloud Providers
          </h3>
          <p className="text-sm text-[var(--text-secondary)] mt-1">
            Configure API keys for cloud-based transcription and diarization services.
          </p>
        </div>

        {message && (
          <div
            className={`mb-4 sm:mb-6 p-3 sm:p-4 rounded-lg flex items-center gap-2 ${
              message.type === "success"
                ? "bg-[var(--success-translucent)] text-[var(--success-solid)]"
                : "bg-[var(--error)]/10 text-[var(--error)]"
            }`}
          >
            {message.type === "success" ? (
              <CheckCircle className="h-4 w-4 flex-shrink-0" />
            ) : (
              <AlertCircle className="h-4 w-4 flex-shrink-0" />
            )}
            {message.text}
          </div>
        )}

        <div className="space-y-4">
          {providers.map((provider) => {
            const info = PROVIDER_INFO[provider.provider] ?? {
              label: provider.provider,
              description: "",
              placeholder: "Enter API key",
            };
            const isSaving = savingProvider === provider.provider;
            const isDeleting = deletingProvider === provider.provider;

            return (
              <Card key={provider.provider} className="bg-[var(--bg-main)] border-[var(--border-subtle)]">
                <CardHeader className="pb-2">
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <CardTitle className="text-base text-[var(--text-primary)]">
                        {info.label}
                      </CardTitle>
                      {provider.has_key && (
                        <span className="text-xs bg-[var(--success-translucent)] text-[var(--success-solid)] px-2 py-0.5 rounded">
                          Configured
                        </span>
                      )}
                    </div>
                    {provider.has_key && (
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => handleDelete(provider.provider)}
                        disabled={isDeleting}
                        aria-label={`Remove ${info.label} API key`}
                        className="text-[var(--text-tertiary)] hover:text-[var(--error)]"
                      >
                        {isDeleting ? (
                          <Loader2 className="h-4 w-4 animate-spin" />
                        ) : (
                          <Trash2 className="h-4 w-4" />
                        )}
                      </Button>
                    )}
                  </div>
                  <CardDescription className="text-[var(--text-secondary)]">
                    {info.description}
                  </CardDescription>
                </CardHeader>
                <CardContent>
                  <div className="flex items-end gap-2">
                    <div className="flex-1">
                      <Label
                        htmlFor={`key-${provider.provider}`}
                        className="text-xs text-[var(--text-tertiary)] flex items-center gap-1"
                      >
                        <Key className="h-3 w-3" />
                        API Key
                      </Label>
                      <Input
                        id={`key-${provider.provider}`}
                        type="password"
                        placeholder={provider.has_key ? "Enter new key to update" : info.placeholder}
                        value={apiKeys[provider.provider] || ""}
                        onChange={(e) =>
                          setApiKeys((prev) => ({ ...prev, [provider.provider]: e.target.value }))
                        }
                        className="mt-1 bg-[var(--bg-main)] border-[var(--border-subtle)] text-[var(--text-primary)] focus:border-[var(--brand-solid)]"
                      />
                    </div>
                    <Button
                      onClick={() => handleSave(provider.provider)}
                      disabled={!apiKeys[provider.provider]?.trim() || isSaving}
                      className="!bg-[var(--brand-gradient)] hover:!opacity-90 !text-black dark:!text-white border-none shadow-lg shadow-black/20"
                    >
                      {isSaving ? (
                        <Loader2 className="h-4 w-4 animate-spin" />
                      ) : (
                        "Save"
                      )}
                    </Button>
                  </div>
                </CardContent>
              </Card>
            );
          })}
        </div>
      </div>
    </div>
  );
}
