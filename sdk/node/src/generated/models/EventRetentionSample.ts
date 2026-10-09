/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Receipt nearing or past its nominal retention deadline, without payloads.
 */
export type EventRetentionSample = {
  event_source: string;
  event_id: string;
  accepted_at: string;
  retain_until: string;
  retained_bytes: number;
  status: 'held' | 'eligible_for_pruning' | 'expiring';
  hold_reason: '' | 'backfill_running' | 'backfill_retryable' | 'recovery_pending';
};

