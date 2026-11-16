/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TaskReviewLimits } from './TaskReviewLimits.js';
/**
 * Offline credential-free Task source review; never executable.
 */
export type TaskReleaseReview = {
  version: 1;
  status: 'review_only';
  release_fingerprint: string;
  protocol: string;
  web_app: string;
  worker_app: string;
  worker_runtime: 'node';
  observer_app: string;
  observer_metric_app: string;
  previous_worker_apps: Array<string>;
  endpoint: string;
  access_mode: string;
  tool_scopes: Record<string, Array<string>>;
  readiness_roles: Array<string>;
  required_setup: Array<string>;
  release_timeout_seconds: number;
  web_configured_limits: TaskReviewLimits;
  worker_configured_limits: TaskReviewLimits;
  /**
   * True because deploying the reviewed Task stack still requires its separately authorized migration workflow.
   */
  migrations_required: boolean;
  /**
   * False because offline source review does not resolve database, key or namespace bindings.
   */
  bindings_verified: boolean;
  /**
   * False because an offline Task review is not an authorized executable release plan.
   */
  ready_to_apply: boolean;
};

