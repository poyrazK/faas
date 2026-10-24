/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Historical automation.recovery_polling_status event. Three consecutive deterministic polling failures trigger failed; unchanged code and policy version stay quiet. Recovered proves a successful poll, independently of workflow outcome. Recipients and payload are frozen at capture; use event_id for deduplication.
 */
export type WorkflowRecoveryPollingWebhookPayload = {
  event_id: string;
  app_id: string;
  app: string;
  scope: 'manual_initial' | 'automatic_policy';
  review_id?: string;
  policy_version: number;
  sequence: number;
  state: 'failed' | 'recovered';
  code: 'invalid_recovery_identity' | 'invalid_observation_data' | 'invalid_policy_authority' | 'invalid_polling_data';
  attempts: number;
  observed_at: string;
  retry_at?: string;
  inspection_url: string;
  /**
   * Always true; successful polling does not prove successful workflow execution.
   */
  monitoring_only: boolean;
};

