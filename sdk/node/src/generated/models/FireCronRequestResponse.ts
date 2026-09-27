/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read shape for `GET /v1/cron-fire-now-requests/{request_id}`.
 *
 */
export type FireCronRequestResponse = {
  request_id: string;
  cron_id: string;
  status: 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled';
  requested_at: string;
  finished_at?: string | null;
  invocation_id?: string | null;
  /**
   * Command task created by this request; absent for HTTP crons or before queueing.
   */
  task_id?: string | null;
  error?: string | null;
  account_id: string;
};

