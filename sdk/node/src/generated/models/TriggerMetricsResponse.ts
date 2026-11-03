/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TriggerConsumerStatus } from './TriggerConsumerStatus.js';
/**
 * Aggregated counters per state for one trigger, and the scheduler's
 * last observation of its broker consumer. NOT a Prometheus
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
  /**
   * Where each count stops; a count equal to it means at least that many.
   */
  count_limit?: number;
  consumer?: TriggerConsumerStatus;
};

