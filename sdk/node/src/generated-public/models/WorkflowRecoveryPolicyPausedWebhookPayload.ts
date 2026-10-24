/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Historical automation.recovery_policy_paused event emitted once per policy version after repeated proven automatic retry failures. Frozen receivers and stable event identity survive retries and later policy updates.
 */
export type WorkflowRecoveryPolicyPausedWebhookPayload = {
  event_id: string;
  app_id: string;
  app: string;
  policy_version: number;
  paused_at: string;
  consecutive_failures: number;
  reason: string;
  policy_url: string;
};

