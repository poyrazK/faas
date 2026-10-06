/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Stable retry identity and the observed dead delivery generation; explicit generation zero is valid.
 */
export type OperationDeliveryRetryRequest = {
  retry_id: string;
  delivery_id: string;
  expected_replay_generation: number;
};

