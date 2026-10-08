/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryCapacityWait } from './EventRecoveryCapacityWait.js';
export type EventRecoveryJobHealth = {
  /**
   * Latest tracked capacity episode. Omitted when no diagnostics have been observed. Preserved while paused; cleared on resume or item progress. Use eligible_at for the next eligible retry.
   */
  capacity_wait?: EventRecoveryCapacityWait;
  job_id: string;
  mode: 'routing' | 'execution';
  state: 'running' | 'paused';
  status: 'running' | 'paused' | 'finishing' | 'expired' | 'stalled' | 'capacity_wait' | 'legacy_claim_wait' | 'paced';
  pending_count: number;
  rate_per_second: number;
  /**
   * Last committed item admission or skip; control changes and capacity deferrals do not advance this timestamp. Omitted when no tracked progress exists.
   */
  last_progress_at?: string;
  /**
   * Age of last tracked progress; falls back to job creation when progress_known is false. This fallback does not reconstruct historical progress.
   */
  progress_age_seconds: number;
  progress_known: boolean;
  next_attempt_at: string;
  /**
   * Later of the scheduled attempt and the end of a spent rate window. Eligibility is ignored while paused.
   */
  eligible_at: string;
  overdue_seconds: number;
  expires_at: string;
  /**
   * Pending work exists and expiry is within one hour or already overdue. Paused jobs retain this signal separately.
   */
  expiring: boolean;
  /**
   * Most recently recorded admission wait; cleared on tracked progress.
   */
  wait_reason?: 'capacity' | 'legacy_claim';
};

