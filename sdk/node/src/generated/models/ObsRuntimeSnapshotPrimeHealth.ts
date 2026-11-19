/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeSnapshotPrimeClassCounts } from './ObsRuntimeSnapshotPrimeClassCounts.js';
import type { ObsRuntimeSnapshotPrimeCounts } from './ObsRuntimeSnapshotPrimeCounts.js';
import type { ObsRuntimeSnapshotPrimeItem } from './ObsRuntimeSnapshotPrimeItem.js';
/**
 * Advisory snapshot-priming evidence for scheduler jobs selected by v2 profiles. Ownership scopes may overlap; never sum attempt counts across replicas. Active or recorded snapshots do not establish deployment readiness.
 */
export type ObsRuntimeSnapshotPrimeHealth = {
  state: 'nonempty' | 'empty' | 'incomplete' | 'unknown';
  reason?: 'snapshot_attempts_observed' | 'incomplete_snapshot_prime_evidence' | 'snapshot_prime_evidence_unavailable';
  counts: ObsRuntimeSnapshotPrimeCounts;
  classes: Array<ObsRuntimeSnapshotPrimeClassCounts>;
  incomplete: boolean;
  items: Array<ObsRuntimeSnapshotPrimeItem>;
  omitted_items: number;
};

