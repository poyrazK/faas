/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeBuildRecoveryCounts } from './ObsRuntimeBuildRecoveryCounts.js';
import type { ObsRuntimeBuildRecoveryItem } from './ObsRuntimeBuildRecoveryItem.js';
/**
 * Advisory running build claims and durable VM cleanup obligations as independently collected API replica views of the shared fleet. Compare views without summing build or cleanup counts. Cleanup age begins at obligation creation and survives retries; evidence is not a VM liveness or deployment-readiness verdict.
 */
export type ObsRuntimeBuildRecoveryHealth = {
  scope: 'replica_observations_of_shared_fleet';
  state: 'nonempty' | 'empty' | 'incomplete' | 'unknown';
  reason?: 'build_recovery_evidence_observed' | 'incomplete_build_recovery_evidence' | 'build_recovery_evidence_unavailable';
  counts: ObsRuntimeBuildRecoveryCounts;
  incomplete: boolean;
  items: Array<ObsRuntimeBuildRecoveryItem>;
  omitted_items: number;
};

