export type SignatureStatus = "none" | "processing" | "ready" | "failed" | string;

export interface Contact {
  id: number;
  vault_id: number;
  contact_uid: string;
  slug: string;
  name: string;
  phone?: string | null;
  email?: string | null;
  notes?: string | null;
  note_path: string;
  file_mtime_ns: number;
  sync_error?: string | null;
  voice_snippet_path?: string | null;
  signature_embedding_path?: string | null;
  signature_status: SignatureStatus;
  signature_data?: string | null;
  created_at: string;
  updated_at: string;
}

export interface ContactsListResponse {
  contacts: Contact[];
  vault_id: number;
}

export interface ContactFilesResponse {
  contact_id: number;
  vault_id: number;
  note_path: string;
  note_abs_path: string;
  voice_snippet_path?: string | null;
  signature_embedding_path?: string | null;
  signature_status: SignatureStatus;
  sync_error?: string | null;
  file_mtime_ns: number;
  voice_snippet_abs_path?: string | null;
  signature_embedding_abs_path?: string | null;
}

export interface ContactAppearance {
  job_id: string;
  title: string;
  speaker_label: string;
  confidence_score: number;
  match_source: string;
}

export interface ContactAppearancesResponse {
  appearances: ContactAppearance[];
}

export interface ContactRequest {
  name: string;
  phone?: string | null;
  email?: string | null;
  notes?: string | null;
}

