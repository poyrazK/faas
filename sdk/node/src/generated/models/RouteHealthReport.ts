/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CanaryProfileSignal } from './CanaryProfileSignal.js';
import type { RouteCustomerHealthReport } from './RouteCustomerHealthReport.js';
import type { RouteHealthFinding } from './RouteHealthFinding.js';
/**
 * Current observed-only critical-route health comparison with candidate, predecessor, policy, and telemetry provenance. When the app's automatic profile policy is enabled, profile_signal adds an ephemeral report-only comparison for an active canary; it never affects canary advancement or rollback and is not persisted.
 */
export type RouteHealthReport = {
  /**
   * Live advisory summary of selected status-code comparisons. Independent of the status used for rollout decisions. Omitted when no codes are selected.
   */
  client_error_status?: 'healthy' | 'regressed' | 'unknown';
  client_error_reason?: string;
  customers?: RouteCustomerHealthReport;
  app_id: string;
  deployment_id: string;
  candidate_commit_sha: string;
  /**
   * Empty when no unique serving predecessor exists.
   */
  stable_deployment_id: string;
  stable_commit_sha: string;
  canary_step: number;
  mode: 'report' | 'enforce';
  /**
   * Recovery policy used for this observation. Abort permits worker recovery on confirmed critical-route errors in enforce mode; reading the report never performs that action.
   */
  on_regression?: 'hold' | 'abort';
  revision: number;
  checked_at: string;
  /**
   * Latest stage or configuration timestamp that both windows must follow.
   */
  observation_anchor?: string;
  coverage: 'observed_only';
  status: 'healthy' | 'regressed' | 'unknown' | 'disabled';
  reason: string;
  minimum_requests: number;
  /**
   * Present when any route has a latency check selected. Applies to each deployment per route per window.
   */
  minimum_latency_requests?: number;
  routes: Array<RouteHealthFinding>;
  profile_signal?: CanaryProfileSignal;
};

