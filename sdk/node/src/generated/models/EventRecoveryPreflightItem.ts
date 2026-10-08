/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type EventRecoveryPreflightItem = {
  position: number;
  status: 'eligible' | 'waiting' | 'likely_skipped' | 'unknown';
  reason: 'eligible' | 'capacity' | 'legacy_claim' | 'changed' | 'expired' | 'target_unavailable' | 'receipt_expired' | 'unknown';
  capacity_scope?: 'account' | 'app' | 'consumer';
};

