/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Compatibility hold and durable recovery progress for a deployment awaiting promotion.
 */
export type DeploymentPromotionHold = {
  code: string;
  detail: string;
  observed_at: string;
  /**
   * First compatibility hold observation.
   */
  held_at: string;
  /**
   * Number of claimed automatic recovery attempts.
   */
  recovery_attempts: number;
  /**
   * Eligibility time; while an attempt runs this is its lease expiry, not a promise of execution.
   */
  next_retry_at?: string;
  recovery_in_progress: boolean;
  last_recovery_outcome?: 'held' | 'retry' | 'recovered' | 'inactive' | 'cancelled';
  last_recovery_at?: string;
  last_recovery_error_code?: string;
};

