/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded active-job diagnostic with inactivity, expiry, progress evidence, and original creation guards.
 */
export type EventExecutionLineageRetryJobHealthSample = {
  request_id: string;
  status: 'pending' | 'paused';
  processed: number;
  selected: number;
  remaining: number;
  /**
   * Original job incarnation guard for operator controls.
   */
  created_at: string;
  /**
   * Authoritative job update timestamp; includes progress and state changes.
   */
  last_activity_at: string;
  inactivity_age_seconds: number;
  /**
   * Last committed case evaluation or skip; absent for unevaluated or legacy jobs.
   */
  last_progress_at?: string;
  /**
   * Age since known last progress or creation before the first evaluation. Absent for legacy evaluated jobs without progress evidence.
   */
  progress_age_seconds?: number;
  progress_known: boolean;
  /**
   * Pending with no job activity for at least 15 minutes; paused jobs never match. Advisory and not proof of scheduler failure.
   */
  stalled_pending: boolean;
  /**
   * Active job expires within 24 hours; includes deliberately paused work.
   */
  retention_risk: boolean;
  retain_until: string;
  retention_seconds: number;
};

