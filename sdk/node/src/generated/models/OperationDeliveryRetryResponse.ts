/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable notification reset decision retained with its parent operation; queued is not delivery success.
 */
export type OperationDeliveryRetryResponse = {
  operation_id: string;
  retry_id: string;
  delivery_id: string;
  expected_replay_generation: number;
  replay_generation: number;
  state: 'queued';
  queued_at: string;
  expires_at: string;
};

