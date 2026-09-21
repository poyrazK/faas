/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CustomMetricResponse } from './CustomMetricResponse.js';
/**
 * The app's ADR-202 gauges, plus the two limits needed to interpret them so debugging does not require reading the docs.
 */
export type CustomMetricListResponse = {
  metrics: Array<CustomMetricResponse>;
  /**
   * A metric older than this reports no signal to the scheduler rather than its last value, so a dead pusher cannot pin the fleet at a frozen backlog.
   */
  freshness_seconds: number;
  /**
   * Maximum distinct metric names this app may hold.
   */
  max_metrics: number;
};

