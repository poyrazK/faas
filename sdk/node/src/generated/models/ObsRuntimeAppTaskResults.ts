/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeAppTaskResultCounts } from './ObsRuntimeAppTaskResultCounts.js';
import type { ObsRuntimeAppTaskResultItem } from './ObsRuntimeAppTaskResultItem.js';
import type { ObsRuntimeAppTaskResultOutcome } from './ObsRuntimeAppTaskResultOutcome.js';
/**
 * Advisory sampled completion acknowledgements and recovery sweep changes for v2 scheduler processes. Completion uses the returned durable status by release, manual or cron kind; queued means a policy retry. Recovery reports only requeued_restores, failed_runs and cancelled from successful sweeps. Failed running tasks can have uncertain command outcomes. These are process-local counter observations, not unique tasks, delivery acknowledgements, infrastructure fault rates or deployment gates. Baselines, resets, changed processes, stale or missing samples establish no delta. Comparisons require unchanged sampled process proof, matching original scrape clocks and uptime advance within the existing one-second tolerance. Sampled continuity cannot rule out every intervening restart.
 */
export type ObsRuntimeAppTaskResults = {
  scope: 'process_local_counter_observations';
  state: 'increased' | 'unchanged' | 'baseline' | 'incomplete' | 'unknown';
  reason?: 'result_increases_observed' | 'comparison_not_requested' | 'incomplete_result_evidence' | 'result_evidence_unavailable';
  comparison_at?: string;
  counts: ObsRuntimeAppTaskResultCounts;
  outcomes: Array<ObsRuntimeAppTaskResultOutcome>;
  incomplete: boolean;
  items: Array<ObsRuntimeAppTaskResultItem>;
  omitted_items: number;
};

