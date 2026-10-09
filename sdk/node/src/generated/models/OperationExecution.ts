/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained execution generation. Exactly one invocation_id or workflow_run_id is present. Workflow attempts counts retained HTTP step attempts at that generation.
 */
export type OperationExecution = {
  generation: number;
  invocation_id?: string;
  job_run_id?: string;
  workflow_run_id?: string;
  state: string;
  attempts: number;
  created_at: string;
  completed_at?: string;
};

