/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Independent completion notification projection from the webhook outbox.
 */
export type OperationDeliveryResponse = {
  state: 'not_requested' | 'awaiting_outcome' | 'configuration_failed' | 'pending' | 'in_flight' | 'succeeded' | 'failed' | 'dead' | 'delivery_expired';
  delivery_id?: string;
  attempts: number;
  last_error?: string;
  next_attempt_at?: string;
};

