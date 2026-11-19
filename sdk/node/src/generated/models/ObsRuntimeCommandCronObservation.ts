/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeCommandCronObservationValues } from './ObsRuntimeCommandCronObservationValues.js';
import type { ObsRuntimeHandoffEvidenceReason } from './ObsRuntimeHandoffEvidenceReason.js';
/**
 * Whether the loop expects independent schedule observation. False on shutdown does not imply empty schedules, disabled cron intent or completed work.
 */
export type ObsRuntimeCommandCronObservation = {
  state: 'available' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeHandoffEvidenceReason;
  observed_at?: string;
  values?: ObsRuntimeCommandCronObservationValues;
};

