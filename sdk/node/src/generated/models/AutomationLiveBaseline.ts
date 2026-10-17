/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationLiveBaselinePolicy } from './AutomationLiveBaselinePolicy.js';
import type { AutomationLiveSuiteSummary } from './AutomationLiveSuiteSummary.js';
/**
 * Immutable attributed receipt binding a named baseline revision to its retained suite and reviewed policy.
 */
export type AutomationLiveBaseline = {
  promotion_id: string;
  app_id: string;
  name: string;
  baseline_name: string;
  revision: number;
  previous_promotion_id?: string;
  actor_account_id: string;
  actor_api_key_id?: string;
  promoted_at: string;
  suite: AutomationLiveSuiteSummary;
  policy: AutomationLiveBaselinePolicy;
  policy_hash: string;
  cases_passed: number;
  oldest_run_created_at: string;
  evidence_age_seconds: number;
};

