/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeCommandCronSweepValues } from './ObsRuntimeCommandCronSweepValues.js';
import type { ObsRuntimeHandoffEvidenceReason } from './ObsRuntimeHandoffEvidenceReason.js';
/**
 * Live shared cron-scan activity, independent of durable schedule collection. A successful scan confirms enumeration and owner lookup, not admission of every command. The scan can include inline HTTP work, so activity alone cannot identify a command-runner failure.
 */
export type ObsRuntimeCommandCronSweep = {
  state: 'available' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeHandoffEvidenceReason;
  observed_at?: string;
  values?: ObsRuntimeCommandCronSweepValues;
};

