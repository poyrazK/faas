/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationLiveBaseline } from './AutomationLiveBaseline.js';
import type { AutomationLiveBaselineCaseHealth } from './AutomationLiveBaselineCaseHealth.js';
/**
 * Coherent read-only evidence health observations for a named baseline head.
 */
export type AutomationLiveBaselineHealth = {
  baseline: AutomationLiveBaseline;
  /**
   * Availability of current recorded reports; not a gate verdict.
   */
  evidence_status: 'usable' | 'failed' | 'unavailable';
  /**
   * Whether this same suite qualifies for a new promotion now.
   */
  renewal_status: 'passed' | 'failed' | 'inconclusive';
  reasons: Array<string>;
  outdated: boolean;
  cases: Array<AutomationLiveBaselineCaseHealth>;
  /**
   * Oldest original retained run age. Negative values indicate future timestamps.
   */
  age_seconds?: number;
  oldest_run_created_at?: string;
  max_age_seconds?: number;
};

