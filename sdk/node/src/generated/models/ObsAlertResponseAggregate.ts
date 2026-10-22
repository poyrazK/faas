/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsAlertResponseCoverage } from './ObsAlertResponseCoverage.js';
import type { ObsAlertResponseMetric } from './ObsAlertResponseMetric.js';
/**
 * One cohort denominator shared by independent acknowledgment and observed recovery classifications.
 */
export type ObsAlertResponseAggregate = {
  episodes: number;
  unresolved: number;
  unknown_observation: number;
  acknowledgment: ObsAlertResponseMetric;
  resolution: ObsAlertResponseMetric;
  receipt_coverage: ObsAlertResponseCoverage;
};

