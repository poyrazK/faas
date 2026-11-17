/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeCommandCronClassCounts } from './ObsRuntimeCommandCronClassCounts.js';
import type { ObsRuntimeCommandCronCounts } from './ObsRuntimeCommandCronCounts.js';
import type { ObsRuntimeCommandCronItem } from './ObsRuntimeCommandCronItem.js';
/**
 * Advisory command-cron schedule and sweep evidence for v2 scheduler jobs. Ownership scopes may overlap; compare scopes without summing schedules. Nonempty includes intentional inactive schedules. Due evidence describes an unconsumed nominal boundary, not a health, eligibility, admission or execution verdict.
 */
export type ObsRuntimeCommandCronHealth = {
  scope: 'scheduler_ownership_scopes';
  state: 'nonempty' | 'empty' | 'incomplete' | 'unknown';
  reason?: 'command_cron_evidence_observed' | 'incomplete_command_cron_evidence' | 'command_cron_evidence_unavailable';
  counts: ObsRuntimeCommandCronCounts;
  classes: Array<ObsRuntimeCommandCronClassCounts>;
  incomplete: boolean;
  items: Array<ObsRuntimeCommandCronItem>;
  omitted_items: number;
};

