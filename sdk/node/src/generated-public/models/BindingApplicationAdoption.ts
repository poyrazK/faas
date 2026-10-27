/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingAdoptionCounts } from './BindingAdoptionCounts.js';
import type { BindingApplicationAckTarget } from './BindingApplicationAckTarget.js';
/**
 * Metadata-only application self-attestations for managed PostgreSQL or object-storage secrets. Counts refer to workload/secret pairs. Reads use a single state snapshot and include missing reports. Task guests, jobs, mirrors and unauthorized workloads are excluded. Receipts expire when secret versions change, independently of probe age. An older guest projection does not erase a newer application receipt. complete describes this optional observation read; a read failure leaves default checks unchanged and blocks strict checks.
 */
export type BindingApplicationAdoption = {
  source: 'application_ack';
  status: 'current' | 'failed' | 'stale' | 'unknown' | 'inactive';
  observed_at: string;
  complete: boolean;
  secrets_expected: number;
  secrets_observed: number;
  reload: BindingAdoptionCounts;
  application: BindingAdoptionCounts;
  targets: Array<BindingApplicationAckTarget>;
};

