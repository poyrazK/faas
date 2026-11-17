/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeBuilderEvidenceReason } from './ObsRuntimeBuilderEvidenceReason.js';
import type { ObsRuntimeBuilderSlotValues } from './ObsRuntimeBuilderSlotValues.js';
/**
 * Process-local reserved build slots; zero usage does not establish free host capacity. Observed_at is the oldest original sample clock of the two gauges.
 */
export type ObsRuntimeBuilderSlots = {
  state: 'available' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeBuilderEvidenceReason;
  observed_at?: string;
  values?: ObsRuntimeBuilderSlotValues;
};

