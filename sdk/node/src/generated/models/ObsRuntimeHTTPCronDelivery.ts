/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeHTTPCronDeliveryCounts } from './ObsRuntimeHTTPCronDeliveryCounts.js';
import type { ObsRuntimeHTTPCronDeliveryItem } from './ObsRuntimeHTTPCronDeliveryItem.js';
import type { ObsRuntimeHTTPCronDeliveryOutcomeSummary } from './ObsRuntimeHTTPCronDeliveryOutcomeSummary.js';
/**
 * Advisory settled HTTP cron delivery attempts for v2 scheduler jobs, including scheduled fires and manual runs. The five ordered outcomes are success, retry, app_error, failed and dead_letter. Application errors remain separate from producer-classified delivery failures. Retry does not establish its cause or final result, and success does not prove unique or exactly-once execution. Exact counter differences require fresh original sample clocks, unchanged sampled process proof and coherent uptime advance within the existing one-second tolerance. Baselines, resets, process changes and incomplete evidence establish no delta. No deployment gate or failure-rate threshold is inferred.
 */
export type ObsRuntimeHTTPCronDelivery = {
  scope: 'process_local_counter_observations';
  state: 'increased' | 'unchanged' | 'baseline' | 'incomplete' | 'unknown';
  reason?: 'http_cron_delivery_increases_observed' | 'comparison_not_requested' | 'incomplete_http_cron_delivery_evidence' | 'http_cron_delivery_evidence_unavailable';
  comparison_at?: string;
  counts: ObsRuntimeHTTPCronDeliveryCounts;
  outcomes: Array<ObsRuntimeHTTPCronDeliveryOutcomeSummary>;
  incomplete: boolean;
  items: Array<ObsRuntimeHTTPCronDeliveryItem>;
  omitted_items: number;
};

