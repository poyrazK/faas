/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeDelayedTaskDispatchCounts } from './ObsRuntimeDelayedTaskDispatchCounts.js';
import type { ObsRuntimeDelayedTaskDispatchItem } from './ObsRuntimeDelayedTaskDispatchItem.js';
import type { ObsRuntimeDelayedTaskDispatchOutcomeSummary } from './ObsRuntimeDelayedTaskDispatchOutcomeSummary.js';
/**
 * Advisory settled delayed-task dispatch attempt counters for v2 scheduler jobs. The four ordered outcomes are success, retry, failed and dead_letter. Failed includes application errors and does not identify a platform incident. Retry does not establish its cause or final result, and success does not prove unique or exactly-once execution. Exact differences require fresh original sample clocks, unchanged sampled process proof and coherent uptime advance within the existing one-second tolerance. Baselines, resets, changed processes and incomplete evidence establish no delta. No deployment gate or failure-rate threshold is inferred.
 */
export type ObsRuntimeDelayedTaskDispatch = {
  scope: 'process_local_counter_observations';
  state: 'increased' | 'unchanged' | 'baseline' | 'incomplete' | 'unknown';
  reason?: 'delayed_task_dispatch_increases_observed' | 'comparison_not_requested' | 'incomplete_delayed_task_dispatch_evidence' | 'delayed_task_dispatch_evidence_unavailable';
  comparison_at?: string;
  counts: ObsRuntimeDelayedTaskDispatchCounts;
  outcomes: Array<ObsRuntimeDelayedTaskDispatchOutcomeSummary>;
  incomplete: boolean;
  items: Array<ObsRuntimeDelayedTaskDispatchItem>;
  omitted_items: number;
};

