/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained execution generation. Exactly one HTTP, workflow, job or data repair backend identity is present. Workflow attempts counts retained HTTP step attempts at that generation.
 */
export type OperationExecution = {
  generation: number;
  invocation_id?: string;
  job_run_id?: string;
  workflow_run_id?: string;
  data_repair_id?: string;
  state: string;
  attempts: number;
  created_at: string;
  completed_at?: string;
};

