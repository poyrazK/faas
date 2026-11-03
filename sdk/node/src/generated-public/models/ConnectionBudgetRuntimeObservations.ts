/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Authenticated workload reports of configured resident Data API pools, including the refresh reader. Not live SQL sessions or independent guest attestation. Parked runtimes and future wakes remain estimates; this does not verify Auth or external consumers.
 */
export type ConnectionBudgetRuntimeObservations = {
  state: 'unknown' | 'partial' | 'observed' | 'not_applicable';
  source: 'workload_report';
  observed_instances: number;
  unobserved_instances: number;
  parked_instances: number;
  configured_connections: number;
  checked_at?: string;
  expires_at?: string;
};

