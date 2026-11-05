/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One completed outbound dispatch, including its replay round and receiver outcome.
 */
export type AppWebhookDeliveryAttemptResponse = {
  id: string;
  delivery_id: string;
  replay_generation: number;
  attempt_number: number;
  outcome: 'succeeded' | 'retrying' | 'dead';
  response_code: number;
  error?: string;
  started_at: string;
  finished_at: string;
  duration_ms: number;
  next_attempt_at?: string;
};

