/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body of POST /v1/uploads response. The session row persists for 24h; the reaper (cmd/apid/upload_session_reaper.go) flips `status='open'` rows whose `expires_at` has passed to 'expired' on a 5-min ticker. `chunk_size` is server-decided (8 MiB default; 16 MiB for Scale).
 */
export type UploadStartResponse = {
  upload_id: string;
  /**
   * Server-decided chunk size in bytes. Default 8 MiB; 16 MiB for Scale.
   */
  chunk_size: number;
  /**
   * Echo of the requested total_size for client confirmation.
   */
  total_size: number;
  /**
   * ISO 8601 UTC timestamp; PATCH after this returns 410 Gone.
   */
  expires_at: string;
};

