export type RuntimeWarmupState = "disabled" | "idle" | "running" | "ready" | "failed";
export type RuntimeWarmupStepStatus = "pending" | "running" | "ready" | "failed";

export interface RuntimeWarmupStep {
  id: string;
  title: string;
  required: boolean;
  status: RuntimeWarmupStepStatus;
  error?: string;
  started_at?: string;
  completed_at?: string;
}

export interface RuntimeWarmupStatus {
  enabled: boolean;
  state: RuntimeWarmupState;
  transcription_ready: boolean;
  voice_signatures_ready: boolean;
  current_step_id?: string;
  current_step_title?: string;
  current_step_detail?: string;
  last_error?: string;
  completed_steps: number;
  total_steps: number;
  completed_required_steps: number;
  total_required_steps: number;
  started_at?: string;
  completed_at?: string;
  updated_at: string;
  steps: RuntimeWarmupStep[];
}
