/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Optional revision-scoped autoscaling override. Omitted CPU target inherits the app policy; 0 explicitly disables CPU-based scale-up for this revision. CPU targets require Pro or Scale.
 */
export type DeploymentScalingRequest = {
  /**
   * Revision CPU scale-up threshold percentage; omit to inherit app policy, 0 to disable, or 1-100 to set a target.
   */
  cpu_utilization_target_pct?: number;
};

