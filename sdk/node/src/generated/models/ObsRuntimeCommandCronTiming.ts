/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeCommandCronTimingCounts } from './ObsRuntimeCommandCronTimingCounts.js';
import type { ObsRuntimeCommandCronTimingItem } from './ObsRuntimeCommandCronTimingItem.js';
import type { ObsRuntimeCommandCronTimingSignalSummary } from './ObsRuntimeCommandCronTimingSignalSummary.js';
/**
 * Advisory process-local command-cron admission timing for v2 scheduler jobs. Timed observations measure returned occurrence CreatedAt minus nominal ScheduledFor, including catch-up history and policy dispositions. This is not database commit time, execution-start or continuous eligibility latency. The eleven cumulative histogram buckets and count require complete coherent snapshots and coherent interval distributions. The unknown-timestamp counter is independent. Exact differences require unchanged sampled process proof, fresh matching original scrape clocks and uptime advance within the existing one-second tolerance. Baselines, missing evidence, restarts and resets establish no timing delta. No observations does not establish zero lag. No percentile, unique task count or deployment gate is inferred.
 */
export type ObsRuntimeCommandCronTiming = {
  scope: 'process_local_counter_observations';
  state: 'increased' | 'unchanged' | 'baseline' | 'incomplete' | 'unknown';
  reason?: 'cron_timing_increases_observed' | 'comparison_not_requested' | 'incomplete_cron_timing_evidence' | 'cron_timing_evidence_unavailable';
  comparison_at?: string;
  counts: ObsRuntimeCommandCronTimingCounts;
  signals: Array<ObsRuntimeCommandCronTimingSignalSummary>;
  incomplete: boolean;
  items: Array<ObsRuntimeCommandCronTimingItem>;
  omitted_items: number;
};

