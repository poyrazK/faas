/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Sync-invoke result. Status is the drain-driven terminal state (`completed` | `failed` | `cancelled`). Result is the original row's payload cast to JSON (omitted while still pending).
 */
export type InvokeResponse = {
  id: string;
  status: 'pending' | 'dispatching' | 'completed' | 'failed' | 'cancelled';
  result?: Record<string, any>;
};

