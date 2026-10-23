/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One command run inside a production fork (ADR-732) and, once finished, its result.
 */
export type AppForkExecResponse = {
  id: string;
  fork_id: string;
  command: Array<string>;
  shell?: boolean;
  timeout_seconds: number;
  max_output_bytes: number;
  status: 'queued' | 'running' | 'succeeded' | 'failed' | 'timed_out';
  exit_code?: number;
  output_truncated: boolean;
  /**
   * Tail of standard output.
   */
  stdout: string;
  /**
   * Tail of standard error.
   */
  stderr: string;
  failure?: {
    code: string;
    message: string;
  };
  requested_by: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
};

