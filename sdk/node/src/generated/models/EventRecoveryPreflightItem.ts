/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Sampled pending recovery item eligibility and current wait or skip diagnostics.
 */
export type EventRecoveryPreflightItem = {
  /**
   * Nominal receipt retention boundary when routing has settled.
   */
  receipt_retain_until?: string;
  /**
   * Current backfill hold; not a promise of future protection.
   */
  receipt_retention_held?: boolean;
  position: number;
  status: 'eligible' | 'waiting' | 'likely_skipped' | 'unknown';
  reason: 'eligible' | 'capacity' | 'legacy_claim' | 'changed' | 'expired' | 'target_unavailable' | 'receipt_expired' | 'unknown';
  capacity_scope?: 'account' | 'app' | 'consumer';
};

