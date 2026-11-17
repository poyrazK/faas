/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeBuildRecoveryCleanup } from './ObsRuntimeBuildRecoveryCleanup.js';
import type { ObsRuntimeBuildRecoveryRunning } from './ObsRuntimeBuildRecoveryRunning.js';
import type { ObsRuntimeHandoffCollection } from './ObsRuntimeHandoffCollection.js';
/**
 * One sanitized API replica view. Collection status, running claims and cleanup obligations are independently checked against source and original clocks. Successful fresh collection is a prerequisite for both work components; valid partial positives survive another component gap.
 */
export type ObsRuntimeBuildRecoveryItem = {
  job: 'apid';
  node?: string;
  target_id?: string;
  unexpected?: boolean;
  scrape_observed_at?: string;
  state: 'available' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  collection: ObsRuntimeHandoffCollection;
  running: ObsRuntimeBuildRecoveryRunning;
  cleanup: ObsRuntimeBuildRecoveryCleanup;
};

