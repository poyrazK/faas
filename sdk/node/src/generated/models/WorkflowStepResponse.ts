/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A step attempt within a durable workflow run.
 */
export type WorkflowStepResponse = {
  step_name: string;
  status: 'pending' | 'running' | 'awaiting_event' | 'succeeded' | 'failed' | 'dead' | 'skipped';
  attempt: number;
  input?: any;
  output?: any;
  started_at?: string | null;
  finished_at?: string | null;
  error?: string | null;
  created_at: string;
};

