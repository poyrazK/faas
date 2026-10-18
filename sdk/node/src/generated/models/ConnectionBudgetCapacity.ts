/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Operator-qualified tenant allocation clamped to observed SQL ceilings for the exact database generation and placement. Unknown without fresh evidence; this does not reserve connections or establish complete consumer coverage.
 */
export type ConnectionBudgetCapacity = {
  state: 'unknown' | 'verified';
  usable_connections?: number;
  source?: 'provider_api' | 'native_qualification';
  checked_at?: string;
  expires_at?: string;
};

