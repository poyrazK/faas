/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Fixed cumulative upper-bound buckets in seconds, followed by the histogram count (timed) and an independent unknown-timestamp counter. le_inf equals timed. Buckets overlap and must not be summed.
 */
export type ObsRuntimeCommandCronTimingName = 'le_1_seconds' | 'le_5_seconds' | 'le_15_seconds' | 'le_30_seconds' | 'le_60_seconds' | 'le_120_seconds' | 'le_300_seconds' | 'le_600_seconds' | 'le_1800_seconds' | 'le_3600_seconds' | 'le_inf' | 'timed' | 'unknown_timestamps';
