/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A receiver from the frozen selection or retained delivery evidence. awaiting_relay requires a retained outbox; otherwise absent delivery evidence is unknown. Attempt counts reset on manual retry; replay_generation distinguishes budgets. No payloads, target URLs, errors, headers or secrets are included.
 */
export type EventRecoveryNotificationReceiver = {
  webhook_id: string;
  /**
   * The selected subscription still exists in this account and app; this does not assert that it is enabled or its target is reachable.
   */
  receiver_available: boolean;
  delivery_id?: string;
  status: 'pending' | 'in_flight' | 'succeeded' | 'failed' | 'dead' | 'awaiting_relay' | 'unknown';
  attempt: number;
  replay_generation: number;
  last_response_code: number;
  next_attempt_at?: string;
  delivered_at?: string;
  /**
   * Relative path to existing paginated attempt history, when the delivery and subscription are retained.
   */
  attempts_path?: string;
  /**
   * Relative POST path for existing independent dead-letter retry, when available. Reporting never invokes it.
   */
  retry_path?: string;
};

