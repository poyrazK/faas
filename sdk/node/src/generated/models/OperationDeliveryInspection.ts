/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Sanitized completion transport snapshot; business state remains independently authoritative.
 */
export type OperationDeliveryInspection = {
  operation_id: string;
  business_state: 'accepted' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'requires_reconciliation';
  operation_expires_at: string;
  observed_at: string;
  state: string;
  delivery_id?: string;
  webhook_id?: string;
  replay_generation?: number;
  attempts: number;
  last_response_code: number;
  error_code?: string;
  next_attempt_at?: string;
  delivered_at?: string;
  receiver_state?: string;
  receiver_cooldown_until?: string;
};

