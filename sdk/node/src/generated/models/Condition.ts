/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One aspect of a resource's state, in the shape of a Kubernetes status condition.
 */
export type Condition = {
  type: 'Ready' | 'Progressing' | 'Degraded' | 'RolledOut';
  status: 'True' | 'False' | 'Unknown';
  /**
   * Stable snake_case code, safe to branch on: an app health phase or check code, a deployment status, or one of not_assessed, deploying, complete, latest_deployment_failed, healthy, verifying, rolling_out, rolled_out, rollout_aborted.
   */
  reason: string;
  /**
   * For people; may change.
   */
  message: string;
  /**
   * When the condition last changed
   */
  since?: string;
  /**
   * True when the condition will not change without a new request.
   */
  terminal?: boolean;
};

