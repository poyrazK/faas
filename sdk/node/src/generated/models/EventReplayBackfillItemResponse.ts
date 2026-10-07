/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Metadata-only outcome for one envelope included in a durable backfill.
 */
export type EventReplayBackfillItemResponse = {
  event_source: string;
  event_id: string;
  event_type: string;
  schema_version?: string;
  accepted_at: string;
  state: 'pending' | 'processing' | 'enqueued' | 'filtered' | 'failed' | 'skipped_captured' | 'skipped_unknown' | 'skipped_existing' | 'skipped_unsettled';
  attempts: number;
  failure_code?: string;
  /**
   * Routing diagnostic clipped to at most 1024 UTF-8 bytes.
   */
  last_error?: string;
  /**
   * Whether the failure code or routing diagnostic was clipped.
   */
  details_truncated?: boolean;
  retryable: boolean;
  updated_at: string;
};

