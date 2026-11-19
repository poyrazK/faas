/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeHandoffEvidenceReason } from './ObsRuntimeHandoffEvidenceReason.js';
import type { ObsRuntimeSnapshotPrimeClassName } from './ObsRuntimeSnapshotPrimeClassName.js';
import type { ObsRuntimeSnapshotPrimeClassValues } from './ObsRuntimeSnapshotPrimeClassValues.js';
/**
 * One ordered snapshot-prime class. Usable values require successful fresh collection after the evidence boundary and the same original scrape clock as collection metadata. Incomplete classes never become empty.
 */
export type ObsRuntimeSnapshotPrimeClass = {
  class: ObsRuntimeSnapshotPrimeClassName;
  state: 'nonempty' | 'empty' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeHandoffEvidenceReason;
  observed_at?: string;
  values?: ObsRuntimeSnapshotPrimeClassValues;
};

