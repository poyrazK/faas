/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Optional revision-scoped scaling controls. Omitted max_concurrent_requests inherits the plan's per-instance request limit; explicit values lower that hard gateway admission cap for this revision. Omitted CPU target inherits the app policy; 0 disables CPU-based scale-up for this revision. CPU targets require Pro or Scale.
 */
export type DeploymentScalingRequest = {
  /**
   * Revision CPU scale-up threshold percentage; omit to inherit app policy, 0 to disable, or 1-100 to set a target.
   */
  cpu_utilization_target_pct?: number;
  /**
   * Maximum simultaneous requests per instance on this revision; must not exceed the plan's concurrency_per_vm limit.
   */
  max_concurrent_requests?: number;
};

