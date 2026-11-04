/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable record of one accepted continuation request.
 */
export type WorkflowResumeResponse = {
  run_id: string;
  resume_number: number;
  account_id: string;
  previous_status: 'failed' | 'dead';
  previous_error?: string;
  resumed_steps: Array<string>;
  created_at: string;
};

