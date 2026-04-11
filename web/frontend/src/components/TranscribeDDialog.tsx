import { useState, useEffect, useCallback } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";
import { Check, ChevronDown, Loader2 } from "lucide-react";
import type { WhisperXParams } from "./TranscriptionConfigDialog";
import { useAuth } from "@/features/auth/hooks/useAuth";

interface TranscriptionProfile {
  id: string;
  name: string;
  description?: string;
  is_default: boolean;
  parameters: WhisperXParams;
  created_at: string;
  updated_at: string;
}

interface TranscribeDDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onStartTranscription: (params: WhisperXParams, profileId?: string) => void;
  loading?: boolean;
  title?: string;
}

export function TranscribeDDialog({
  open,
  onOpenChange,
  onStartTranscription,
  loading = false,
  title,
}: TranscribeDDialogProps) {
  const { getAuthHeaders } = useAuth();
  const [profiles, setProfiles] = useState<TranscriptionProfile[]>([]);
  const [selectedProfileId, setSelectedProfileId] = useState<string>("");
  const [profilesLoading, setProfilesLoading] = useState(false);
  const [defaultProfile, setDefaultProfile] = useState<TranscriptionProfile | null>(null);
  const [profilePopoverOpen, setProfilePopoverOpen] = useState(false);

  const fetchProfiles = useCallback(async () => {
    try {
      setProfilesLoading(true);

      // Fetch all profiles
      const profilesResponse = await fetch("/api/v1/profiles", {
        headers: {
          ...getAuthHeaders(),
        },
      });

      if (profilesResponse.ok) {
        const profilesData: TranscriptionProfile[] = await profilesResponse.json();
        setProfiles(profilesData);

        // Fetch user's default profile
        const defaultResponse = await fetch("/api/v1/user/default-profile", {
          headers: {
            ...getAuthHeaders(),
          },
        });

        if (defaultResponse.ok) {
          const defaultData: TranscriptionProfile = await defaultResponse.json();
          setDefaultProfile(defaultData);
          setSelectedProfileId(defaultData.id);
        } else if (defaultResponse.status === 404) {
          // No default profile set, use the first available profile
          setDefaultProfile(null);
          if (profilesData.length > 0) {
            setSelectedProfileId(profilesData[0].id);
          }
        }
      } else {
        console.error("Failed to fetch profiles");
      }
    } catch (error) {
      console.error("Error fetching profiles:", error);
    } finally {
      setProfilesLoading(false);
    }
  }, [getAuthHeaders]);

  // Fetch profiles when dialog opens
  useEffect(() => {
    if (open) {
      fetchProfiles();
    }
  }, [open, fetchProfiles]);

  const handleStartTranscription = () => {
    if (!selectedProfileId) return;

    const selectedProfile = profiles.find(p => p.id === selectedProfileId);
    if (selectedProfile) {
      onStartTranscription(selectedProfile.parameters, selectedProfile.id);
    }
  };

  const handleProfileChange = (value: string) => {
    setSelectedProfileId(value);
  };

  const selectedProfile = profiles.find((profile) => profile.id === selectedProfileId);
  const selectedProfileLabel = selectedProfile
    ? `${selectedProfile.name}${defaultProfile && selectedProfile.id === defaultProfile.id ? " (Default)" : ""}`
    : "";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg min-w-0 overflow-hidden glass-card rounded-[var(--radius-card)] p-0 gap-0 border border-[var(--border-subtle)] shadow-[var(--shadow-float)]">
        <DialogHeader className="min-w-0 p-6 pb-2">
          <DialogTitle className="min-w-0 text-xl font-bold tracking-tight text-[var(--text-primary)]">
            {title || "Transcribe with Profile"}
          </DialogTitle>
          <DialogDescription className="mt-1.5 min-w-0 pr-8 text-sm leading-relaxed text-[var(--text-secondary)]">
            Choose a saved profile to start transcription with your preferred settings.
          </DialogDescription>
        </DialogHeader>



        <div className="min-w-0 space-y-4 px-6 py-2">
          <div className="min-w-0 space-y-2">
            <Label htmlFor="profile" className="text-[var(--text-secondary)] font-medium">
              Select Profile
            </Label>

            {profilesLoading ? (
              <div className="flex items-center space-x-2 p-3 bg-[var(--bg-main)]/50 rounded-[var(--radius-btn)] border border-[var(--border-subtle)]">
                <Loader2 className="h-4 w-4 animate-spin text-[var(--text-tertiary)]" />
                <span className="text-sm text-[var(--text-secondary)]">Loading profiles...</span>
              </div>
            ) : profiles.length === 0 ? (
              <div className="p-3 bg-[var(--bg-main)]/50 rounded-[var(--radius-btn)] border border-[var(--border-subtle)]">
                <span className="text-sm text-[var(--text-secondary)]">No profiles available</span>
              </div>
            ) : (
              <Popover open={profilePopoverOpen} onOpenChange={setProfilePopoverOpen} modal={true}>
                <PopoverTrigger asChild>
                  <button
                    type="button"
                    className="h-11 w-full min-w-0 overflow-hidden rounded-[var(--radius-btn)] border border-[var(--border-subtle)] bg-[var(--bg-main)] px-4 text-sm text-[var(--text-primary)] shadow-none outline-none transition-all hover:border-[var(--brand-solid)]/50 focus:ring-2 focus:ring-[var(--brand-solid)]/20 inline-flex items-center justify-between"
                    aria-label="Choose profile"
                  >
                    <span className="block min-w-0 flex-1 truncate text-left">
                      {selectedProfileLabel || "Choose a profile..."}
                    </span>
                    <ChevronDown className="ml-2 h-4 w-4 shrink-0 opacity-50" />
                  </button>
                </PopoverTrigger>
                <PopoverContent
                  className="w-[min(var(--radix-popover-trigger-width),calc(100vw-4rem))] max-w-[calc(100vw-4rem)] rounded-[var(--radius-card)] border border-[var(--border-subtle)] bg-[var(--bg-card)] p-1 shadow-[var(--shadow-float)]"
                  onOpenAutoFocus={(e) => e.preventDefault()}
                >
                  <Command className="bg-transparent">
                    <CommandInput placeholder="Search profiles..." className="h-10 border-none focus:ring-0" />
                    <CommandList className="max-h-64 overflow-auto p-1">
                      <CommandEmpty className="py-3 text-center text-xs text-[var(--text-tertiary)]">
                        No profiles found
                      </CommandEmpty>
                      <CommandGroup heading="Profiles" className="text-[var(--text-tertiary)]">
                        {profiles.map((profile) => {
                          const isDefaultProfile = defaultProfile?.id === profile.id;
                          const isSelected = selectedProfileId === profile.id;

                          return (
                            <CommandItem
                              key={profile.id}
                              value={`${profile.name} ${profile.description ?? ""}`}
                              onSelect={() => {
                                handleProfileChange(profile.id);
                                setProfilePopoverOpen(false);
                              }}
                              className="cursor-pointer rounded-lg px-3 py-2.5 aria-selected:bg-[var(--brand-light)] aria-selected:text-[var(--brand-solid)]"
                            >
                              <div className="flex min-w-0 flex-1 flex-col">
                                <div className="flex min-w-0 items-center gap-2">
                                  <span className="min-w-0 flex-1 truncate text-sm font-medium">
                                    {profile.name}
                                  </span>
                                  {isDefaultProfile && (
                                    <span className="shrink-0 rounded bg-[var(--success-translucent)] px-1.5 py-0.5 text-xs text-[var(--success-solid)]">
                                      Default
                                    </span>
                                  )}
                                </div>
                                {profile.description && (
                                  <span className="truncate text-xs text-[var(--text-tertiary)]">
                                    {profile.description}
                                  </span>
                                )}
                              </div>
                              <Check
                                className={cn(
                                  "ml-2 h-4 w-4 shrink-0",
                                  isSelected ? "opacity-100" : "opacity-0"
                                )}
                              />
                            </CommandItem>
                          );
                        })}
                      </CommandGroup>
                    </CommandList>
                  </Command>
                </PopoverContent>
              </Popover>
            )}
          </div>


        </div>

        <DialogFooter className="p-6 pt-2 gap-3">
          <Button
            variant="ghost"
            onClick={() => onOpenChange(false)}
            className="rounded-[var(--radius-btn)] text-[var(--text-secondary)] hover:bg-[var(--secondary)] hover:text-[var(--text-primary)]"
          >
            Cancel
          </Button>
          <Button
            onClick={handleStartTranscription}
            disabled={loading || !selectedProfileId || profilesLoading || profiles.length === 0}
            className="min-w-[140px] !bg-[var(--brand-gradient)] hover:!opacity-90 !text-black dark:!text-white border-none shadow-lg shadow-black/20"
          >
            {loading ? (
              <>
                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                Starting...
              </>
            ) : (
              "Start Transcription"
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog >
  );
}
