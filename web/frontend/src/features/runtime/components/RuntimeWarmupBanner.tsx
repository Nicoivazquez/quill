import { useEffect, useMemo, useRef, useState } from "react";
import { AlertTriangle, CheckCircle2, ChevronDown, ChevronUp, Loader2, RefreshCw, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import { useRuntimeWarmup, useRetryRuntimeWarmup } from "@/features/runtime/hooks/useRuntimeWarmup";
import type { RuntimeWarmupStatus, RuntimeWarmupStep, RuntimeWarmupStepStatus } from "@/features/runtime/types";

function stepStatusLabel(status: RuntimeWarmupStepStatus): string {
  switch (status) {
    case "ready":
      return "ready";
    case "running":
      return "running";
    case "failed":
      return "failed";
    default:
      return "pending";
  }
}

function stepStatusClass(status: RuntimeWarmupStepStatus): string {
  switch (status) {
    case "ready":
      return "border-emerald-500/25 bg-emerald-500/10 text-emerald-300";
    case "running":
      return "border-sky-500/25 bg-sky-500/10 text-sky-300";
    case "failed":
      return "border-rose-500/25 bg-rose-500/10 text-rose-300";
    default:
      return "border-[var(--border-subtle)] bg-[var(--bg-main)] text-[var(--text-tertiary)]";
  }
}

function bannerCopy(status: RuntimeWarmupStatus): {
  title: string;
  detail: string;
  toneClass: string;
  indicatorClassName: string;
} {
  if (status.state === "failed" && status.transcription_ready) {
    return {
      title: "Transcription is ready, but optional local AI tools need retry",
      detail: status.last_error || "Local speaker or voice-signature setup did not finish cleanly.",
      toneClass: "border-amber-500/30 bg-amber-500/10",
      indicatorClassName: "bg-amber-400",
    };
  }

  if (status.state === "failed") {
    return {
      title: "Preparing local transcription models failed",
      detail: status.last_error || "Quill could not finish installing its local transcription runtime.",
      toneClass: "border-rose-500/30 bg-rose-500/10",
      indicatorClassName: "bg-rose-400",
    };
  }

  if (status.transcription_ready) {
    return {
      title: "Transcription is ready",
      detail:
        status.current_step_detail ||
        "Quill is finishing local speaker and voice-signature tools in the background. Basic transcription is ready now.",
      toneClass: "border-emerald-500/25 bg-emerald-500/10",
      indicatorClassName: "bg-emerald-400",
    };
  }

  return {
    title: "Preparing local transcription models",
    detail:
      status.current_step_detail ||
      "First launch downloads the local runtime and default speech model. This can take several minutes.",
    toneClass: "border-sky-500/25 bg-sky-500/10",
    indicatorClassName: "bg-sky-400",
  };
}

function StepRow({ step }: { step: RuntimeWarmupStep }) {
  return (
    <div className="flex items-center justify-between gap-3 rounded-[var(--radius-btn)] border border-[var(--border-subtle)] bg-[var(--bg-main)] px-3 py-2">
      <div className="min-w-0">
        <div className="text-sm font-medium text-[var(--text-primary)]">{step.title}</div>
        <div className="mt-1 flex items-center gap-2 text-[11px] uppercase tracking-[0.16em] text-[var(--text-tertiary)]">
          <span>{step.required ? "required" : "optional"}</span>
          {step.error ? <span className="truncate normal-case tracking-normal text-[var(--text-secondary)]">{step.error}</span> : null}
        </div>
      </div>
      <span className={`shrink-0 rounded-full border px-2 py-1 text-[10px] font-semibold uppercase tracking-[0.14em] ${stepStatusClass(step.status)}`}>
        {stepStatusLabel(step.status)}
      </span>
    </div>
  );
}

export function RuntimeWarmupBanner() {
  const { data, isLoading } = useRuntimeWarmup();
  const retryWarmup = useRetryRuntimeWarmup();
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [dismissedOptional, setDismissedOptional] = useState(false);
  const warmupState = data?.state;
  const transcriptionReady = data?.transcription_ready ?? false;

  const prevWarmupState = useRef(warmupState);
  useEffect(() => {
    if (prevWarmupState.current !== warmupState && warmupState !== "running") {
      setDismissedOptional(false);
    }
    prevWarmupState.current = warmupState;
  }, [warmupState]);

  const progressValue = useMemo(() => {
    if (!data || data.total_steps === 0) {
      return 0;
    }
    return Math.max(8, Math.round((data.completed_steps / data.total_steps) * 100));
  }, [data]);

  if (isLoading || !data?.enabled) {
    return null;
  }

  if (data.state !== "running" && data.state !== "failed") {
    return null;
  }

  if (dismissedOptional && warmupState === "running" && transcriptionReady) {
    return null;
  }

  const copy = bannerCopy(data);
  const showDismiss = warmupState === "running" && transcriptionReady;

  return (
    <div className={`mt-3 rounded-[calc(var(--radius-card)-6px)] border px-3 py-3 sm:px-4 ${copy.toneClass}`}>
      <div className="flex flex-col gap-3">
        <div className="flex items-start justify-between gap-3">
          <div className="flex min-w-0 gap-3">
            <div className="mt-0.5 shrink-0 text-[var(--text-primary)]">
              {data.state === "failed" ? (
                <AlertTriangle className="h-4 w-4 text-amber-300" />
              ) : data.transcription_ready ? (
                <CheckCircle2 className="h-4 w-4 text-emerald-300" />
              ) : (
                <Loader2 className="h-4 w-4 animate-spin text-sky-300" />
              )}
            </div>
            <div className="min-w-0">
              <div className="text-sm font-semibold text-[var(--text-primary)]">{copy.title}</div>
              <p className="mt-1 text-sm text-[var(--text-secondary)]">{copy.detail}</p>
              <p className="mt-1 text-xs text-[var(--text-tertiary)]">
                {data.transcription_ready
                  ? "You can transcribe now. Speaker and voice-signature tooling may continue downloading in the background."
                  : "Quill will be ready to transcribe as soon as the required steps finish."}
              </p>
            </div>
          </div>

          <div className="flex shrink-0 items-center gap-2">
            {data.state === "failed" ? (
              <Button
                variant="outline"
                size="sm"
                onClick={() => retryWarmup.mutate()}
                disabled={retryWarmup.isPending}
                className="border-[var(--border-subtle)] bg-[var(--bg-card)]/70 text-[var(--text-primary)] hover:bg-[var(--bg-card)]"
              >
                {retryWarmup.isPending ? <Loader2 className="mr-2 h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="mr-2 h-3.5 w-3.5" />}
                Retry
              </Button>
            ) : null}
            {showDismiss ? (
              <Button
                variant="ghost"
                size="icon"
                onClick={() => setDismissedOptional(true)}
                className="h-8 w-8 text-[var(--text-secondary)] hover:bg-[var(--bg-card)]/70"
              >
                <X className="h-4 w-4" />
                <span className="sr-only">Dismiss runtime status</span>
              </Button>
            ) : null}
          </div>
        </div>

        <div className="flex items-center gap-3">
          <Progress
            value={progressValue}
            className="h-2 flex-1 bg-[var(--bg-card)]/40"
            indicatorClassName={copy.indicatorClassName}
          />
          <div className="shrink-0 text-xs font-medium text-[var(--text-secondary)]">
            {data.completed_steps}/{data.total_steps} steps
          </div>
        </div>

        <div className="flex items-center justify-between">
          <div className="text-[11px] uppercase tracking-[0.16em] text-[var(--text-tertiary)]">
            {data.completed_required_steps}/{data.total_required_steps} required ready
          </div>
          <button
            type="button"
            onClick={() => setDetailsOpen((value) => !value)}
            className="inline-flex items-center gap-1 text-xs font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
          >
            {detailsOpen ? "Hide details" : "Show details"}
            {detailsOpen ? <ChevronUp className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" />}
          </button>
        </div>

        {detailsOpen ? (
          <div className="grid gap-2">
            {data.steps.map((step) => (
              <StepRow key={step.id} step={step} />
            ))}
          </div>
        ) : null}
      </div>
    </div>
  );
}
