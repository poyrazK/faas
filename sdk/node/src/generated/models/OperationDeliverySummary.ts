/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Independent notification status without delivery identifiers or errors.
 */
export type OperationDeliverySummary = {
  state: 'not_requested' | 'awaiting_outcome' | 'configuration_failed' | 'pending' | 'in_flight' | 'succeeded' | 'failed' | 'dead' | 'delivery_expired';
  attempts: number;
  next_attempt_at?: string;
};

