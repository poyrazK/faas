/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeCommandCronOutcomeName } from './ObsRuntimeCommandCronOutcomeName.js';
import type { ObsRuntimeCommandCronOutcomeSource } from './ObsRuntimeCommandCronOutcomeSource.js';
/**
 * Coverage for one ordered source/outcome across all inspected scheduler processes. Sweep has success, error, canceled. Decision has disabled, not_due, account_held, plan_limit, replacement_wait, not_owned, invalid_schedule, lookup_error, store_unavailable, no_live_deployment, canceled, legacy_queued, legacy_skipped, legacy_stale, legacy_error. Admission has queued, exclusive_pending, coalesced, skipped_overlap, missed_deadline, stale, deployment_unavailable, rejected, error, canceled, unknown. Counts describe signals with observed changes, not numbers of unique occurrences or calls.
 */
export type ObsRuntimeCommandCronOutcomeSummary = {
  source: ObsRuntimeCommandCronOutcomeSource;
  outcome: ObsRuntimeCommandCronOutcomeName;
  increased: number;
  unchanged: number;
  baseline: number;
  unknown: number;
};

