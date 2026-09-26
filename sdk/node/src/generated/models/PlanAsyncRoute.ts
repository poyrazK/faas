/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RetryPolicyDTO } from './RetryPolicyDTO.js';
/**
 * One manifest-owned async-route reconciliation row in a project deployment plan. Route fields describe the planned route, or the existing route for a removal.
 */
export type PlanAsyncRoute = {
  /**
   * Workload or app slug that owns the route
   */
  app: string;
  /**
   * Stable async-route identity within the app
   */
  name: string;
  /**
   * Reconciliation result; skipped means selection flags or --no-triggers leave this route untouched.
   */
  action: 'create' | 'update' | 'remove' | 'unchanged' | 'skipped';
  match_host: string;
  match_path: string;
  match_methods: Array<string>;
  priority: number;
  enabled: boolean;
  /**
   * App webhook destination for successful executions
   */
  on_success?: string;
  /**
   * App webhook destination for failed executions
   */
  on_failure?: string;
  retry_policy?: RetryPolicyDTO;
  max_age_seconds?: number;
  /**
   * Why this route is skipped, or what reconciliation does
   */
  reason?: string;
};

