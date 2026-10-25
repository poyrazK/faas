/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Sync-invoke result. Status is the drain-driven terminal state (`completed` | `failed` | `cancelled`). Result is the handler response. Error contains the terminal delivery error when status is failed.
 */
export type InvokeResponse = {
  id: string;
  status: 'pending' | 'dispatching' | 'completed' | 'failed' | 'cancelled';
  result?: Record<string, any>;
  error?: string;
};

