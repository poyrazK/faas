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
   * Always true for a retained hosted source intent.
   */
  migrations_required: boolean;
  /**
   * Always false; source retention grants no execution authority.
   */
  bindings_verified: boolean;
  /**
   * Always false; source retention grants no execution authority.
   */
  ready_to_apply: boolean;
};

