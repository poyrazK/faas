/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One aspect of a resource's state, in the shape of a Kubernetes status condition.
 */
export type Condition = {
  type: 'Ready' | 'Progressing' | 'Degraded' | 'RolledOut' | 'Verified' | 'CertificateReady' | 'LastRunSucceeded' | 'Succeeded';
  status: 'True' | 'False' | 'Unknown';
  /**
   * Stable snake_case code, safe to branch on: an app health phase or check code; a deployment, database, certificate or job run status; a cron suspension or run outcome; a database health error code; or one of not_assessed, deploying, complete, latest_deployment_failed, healthy, verifying, rolling_out, rolled_out, rollout_aborted, suspended, stale, verified, dns_pending, serving, scheduled, disabled, never_run.
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

