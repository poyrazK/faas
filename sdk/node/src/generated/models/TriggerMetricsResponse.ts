/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Aggregated counters per state for one trigger. NOT a Prometheus
 * scrape — /v1/metrics is the Prometheus surface.
 *
 */
export type TriggerMetricsResponse = {
  trigger_id: string;
  pending_count: number;
  claimed_count: number;
  succeeded_count: number;
  retry_count: number;
  dead_letter_count: number;
};

