/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Frozen terminal recovery admission metadata. Completed means recovery admission finished; admitted handler executions and routing retries can continue. Expired uses stored cancelled state with distinct expired outcome. JSON delivery nests this payload under payload.data; CloudEvents uses data. The event_id is stable across receivers and attempts, independently of each receiver delivery ID.
 */
export type EventRecoveryFinishedWebhookPayload = {
  event_id: string;
  job_id: string;
  app_id: string;
  mode: 'routing' | 'execution';
  state: 'completed' | 'cancelled';
  outcome: 'completed' | 'cancelled' | 'expired';
  selected_count: number;
  pending_count: 0;
  queued_count: number;
  skipped_count: number;
  cancelled_count: number;
  created_at: string;
  expires_at: string;
  completed_at: string;
};

