/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteHealthFinding } from './RouteHealthFinding.js';
export type RouteHealthReport = {
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
   * Defaults to hold, including when omitted in a replacement update. Abort opts into automatic recovery for confirmed route 5xx regressions during an enforced canary; report mode is observational.
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
};

