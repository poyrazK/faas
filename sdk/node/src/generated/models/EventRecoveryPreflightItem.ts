/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Sampled pending recovery eligibility or a saved native workflow retry target, with current wait and skip diagnostics.
 */
export type EventRecoveryPreflightItem = {
  workflow_run_id?: string;
  /**
   * Original reviewed count for pending native items; admitted parent count for retry targets. Non-admitted items may exceed the resume limit and are never selectable.
   */
  expected_resume_count?: number;
  saved_outcome?: 'queued' | 'running' | 'retrying' | 'succeeded' | 'failed' | 'dead_lettered' | 'expired' | 'cancelled' | 'superseded' | 'unknown';
  /**
   * Safe resume blocker codes without workflow payloads or provider errors.
   */
  blockers?: Array<string>;
  /**
   * Nominal receipt retention boundary when routing has settled.
   */
  receipt_retain_until?: string;
  /**
   * Current backfill or recovery hold; ends when its owning job or item releases it.
   */
  receipt_retention_held?: boolean;
  position: number;
  status: 'eligible' | 'waiting' | 'likely_skipped' | 'unknown';
  reason: 'eligible' | 'capacity' | 'legacy_claim' | 'changed' | 'expired' | 'target_unavailable' | 'receipt_expired' | 'unknown' | 'not_admitted' | 'outcome_not_saved' | 'outcome_not_failed' | 'run_not_retained' | 'resume_ineligible' | 'resume_limit_reached';
  capacity_scope?: 'account' | 'app' | 'consumer';
};

