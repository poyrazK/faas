/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryOverviewAlert } from './WorkflowRecoveryOverviewAlert.js';
import type { WorkflowRecoveryOverviewIssue } from './WorkflowRecoveryOverviewIssue.js';
/**
 * Read-only retained recovery observations from one snapshot. Covers manual initial reviews, manual retry disposition/completion observations and the automatic queue, not all workflow outcomes or scheduler liveness. Counts are exact; issues are bounded. Delivery covers the last retained polling alert only.
 */
export type WorkflowRecoveryOverview = {
  /**
   * Undecided current enabled preview retry plans requiring a live status inspection; no freshness or readiness guarantee.
   */
  manual_retry_awaiting_review?: number;
  /**
   * Approved retry reviews without a saved completion observation, including unapplied plans and reviews from older policy versions.
   */
  manual_retry_unresolved?: number;
  status: 'healthy' | 'waiting' | 'paused' | 'needs_attention' | 'inactive' | 'unknown';
  monitoring_status: 'no_observed_failure' | 'failing' | 'unknown';
  observed_at: string;
  monitoring_only: boolean;
  manual_awaiting_approval: number;
  manual_unresolved: number;
  manual_polling_failures: number;
  manual_attention: number;
  truncated: boolean;
  issues: Array<WorkflowRecoveryOverviewIssue>;
  last_alert?: WorkflowRecoveryOverviewAlert;
};

