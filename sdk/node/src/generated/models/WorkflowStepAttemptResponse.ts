/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One durable executor invocation for a workflow step.
 */
export type WorkflowStepAttemptResponse = {
  attempt: number;
  status: 'running' | 'retrying' | 'succeeded' | 'failed';
  http_status?: number | null;
  started_at: string;
  finished_at?: string | null;
  next_attempt_at?: string | null;
  error?: string | null;
};

