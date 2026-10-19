/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsAlertRecurrenceMetrics } from './ObsAlertRecurrenceMetrics.js';
/**
 * Complete cohort counts across distinct rule-definition and exact-series groups.
 */
export type ObsAlertRecurrenceTotals = {
  series: number;
  repeated_series: number;
  metrics: ObsAlertRecurrenceMetrics;
};

