/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeAppTaskOperationCounts } from './ObsRuntimeAppTaskOperationCounts.js';
import type { ObsRuntimeAppTaskOperationItem } from './ObsRuntimeAppTaskOperationItem.js';
import type { ObsRuntimeAppTaskOperationSummary } from './ObsRuntimeAppTaskOperationSummary.js';
/**
 * Advisory observed operation counter changes for v2 scheduler jobs. Counters belong to individual processes. Five operations and seven outcomes are inspected without treating missing capacity series as zero. First samples are baselines, not newly observed failures. Deltas require unchanged sampled process identity, fresh points at both pinned times and matching original scrape clocks. Uptime advance must match elapsed scrape time within the existing one-second reset tolerance. Sampled continuity cannot rule out every intervening restart. These are observed sample differences, not rates, exact event times or command verdicts.
 */
export type ObsRuntimeAppTaskOperations = {
  scope: 'process_local_counter_observations';
  state: 'increased' | 'unchanged' | 'baseline' | 'incomplete' | 'unknown';
  reason?: 'operation_increases_observed' | 'comparison_not_requested' | 'incomplete_operation_evidence' | 'operation_evidence_unavailable';
  comparison_at?: string;
  counts: ObsRuntimeAppTaskOperationCounts;
  operations: Array<ObsRuntimeAppTaskOperationSummary>;
  incomplete: boolean;
  items: Array<ObsRuntimeAppTaskOperationItem>;
  omitted_items: number;
};

