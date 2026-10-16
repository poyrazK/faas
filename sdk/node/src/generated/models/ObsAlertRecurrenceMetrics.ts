/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsAlertRecurrenceDuration } from './ObsAlertRecurrenceDuration.js';
import type { ObsAlertRecurrenceGap } from './ObsAlertRecurrenceGap.js';
/**
 * Episode denominator and adjacent-repeat denominator with independent evidence partitions.
 */
export type ObsAlertRecurrenceMetrics = {
  episodes: number;
  repeat_activations: number;
  unresolved: number;
  unknown_observation: number;
  duration: ObsAlertRecurrenceDuration;
  recovery_gap: ObsAlertRecurrenceGap;
};

