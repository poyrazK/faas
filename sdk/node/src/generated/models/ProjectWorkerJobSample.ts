/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Attributed delivery attempts in a bounded window, including unknown unfinished deliveries.
 */
export type ProjectWorkerJobSample = {
  attempts: number;
  failures: number;
  incomplete: number;
  duration_ms: number;
  retries: number;
  queue_delay_ms: number;
};

