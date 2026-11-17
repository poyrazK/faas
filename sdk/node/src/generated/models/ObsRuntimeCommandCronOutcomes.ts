/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeCommandCronOutcomeCounts } from './ObsRuntimeCommandCronOutcomeCounts.js';
import type { ObsRuntimeCommandCronOutcomeItem } from './ObsRuntimeCommandCronOutcomeItem.js';
import type { ObsRuntimeCommandCronOutcomeSummary } from './ObsRuntimeCommandCronOutcomeSummary.js';
/**
 * Advisory process-local cron sweep, scheduling-decision and admission-return counters for v2 scheduler jobs. The 29 fixed source/outcome signals are independent. Sweep success covers a shared scan including inline HTTP work and does not prove every command was admitted. Decisions are not unique due occurrences; queued, exclusive_pending and coalesced returns do not prove command execution or delivery. Baselines, changed processes, resets, stale or missing evidence establish no delta. Exact differences require unchanged sampled process proof, matching original scrape clocks and uptime advance within the existing one-second tolerance. Sampled continuity cannot exclude every intervening restart. No scheduling policy, rate or deployment gate is inferred.
 */
export type ObsRuntimeCommandCronOutcomes = {
  scope: 'process_local_counter_observations';
  state: 'increased' | 'unchanged' | 'baseline' | 'incomplete' | 'unknown';
  reason?: 'cron_outcome_increases_observed' | 'comparison_not_requested' | 'incomplete_cron_outcome_evidence' | 'cron_outcome_evidence_unavailable';
  comparison_at?: string;
  counts: ObsRuntimeCommandCronOutcomeCounts;
  outcomes: Array<ObsRuntimeCommandCronOutcomeSummary>;
  incomplete: boolean;
  items: Array<ObsRuntimeCommandCronOutcomeItem>;
  omitted_items: number;
};

