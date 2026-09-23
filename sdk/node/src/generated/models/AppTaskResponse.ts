/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppTaskFailure } from './AppTaskFailure.js';
/**
 * App-scoped receipt for a deployment-attached command. Scheduler lease
 * data, rootfs storage keys, and image digests are intentionally omitted.
 *
 */
export type AppTaskResponse = {
  id: string;
  app_id: string;
  deployment_id: string;
  deployment_scope: string;
  kind: 'manual' | 'release';
  command: Array<string>;
  command_shell: boolean;
  status: 'queued' | 'restoring' | 'running' | 'succeeded' | 'failed' | 'timed_out' | 'cancelled';
  timeout_seconds: number;
  max_output_bytes: number;
  stdout_tail?: string;
  stderr_tail?: string;
  output_truncated: boolean;
  exit_code?: number | null;
  failure?: (AppTaskFailure | null);
  cancel_requested_at?: string | null;
  started_at?: string | null;
  finished_at?: string | null;
  created_at: string;
  updated_at: string;
};

