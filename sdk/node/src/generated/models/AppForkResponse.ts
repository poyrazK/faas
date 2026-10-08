/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One production fork (ADR-732). `status` moves from `queued` through
 * `restoring` to `running`, and ends as `expired`, `cancelled` or
 * `failed`. Scheduler leases and instance identifiers are never
 * returned.
 *
 */
export type AppForkResponse = {
  id: string;
  app_id: string;
  /**
   * Returned only by `POST /v1/apps/{slug}/forks`, never again. To
   * reach the running fork, send a request to the app's hostname with
   * `X-Gregale-Fork: <id>` and `X-Gregale-Fork-Token: <access_token>`.
   * The gateway routes it to the fork, strips both headers, and never
   * wakes the app for it.
   *
   */
  access_token?: string;
  /**
   * The live deployment pinned when the fork was requested.
   */
  deployment_id: string;
  status: 'queued' | 'restoring' | 'running' | 'expired' | 'cancelled' | 'failed';
  ttl_seconds: number;
  /**
   * When the fork is destroyed (created_at + ttl_seconds).
   */
  expires_at: string;
  cancel_requested_at?: string;
  started_at?: string;
  finished_at?: string;
  failure?: {
    code: string;
    message: string;
  };
  created_at: string;
  updated_at: string;
};

