/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One retained notification attempt without receiver payload, URL or raw transport errors.
 */
export type OperationDeliveryAttempt = {
  replay_generation: number;
  attempt_number: number;
  outcome: 'succeeded' | 'retrying' | 'dead';
  response_code: number;
  error_code?: string;
  started_at: string;
  finished_at: string;
  next_attempt_at?: string;
};

