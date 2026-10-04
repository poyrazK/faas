/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Server-managed workflow lifecycle metadata. Source and input are not
 * returned. `next_step` is the number of steps already admitted as Runs,
 * including parallel admissions.
 *
 */
export type ManagedExecutionWorkflowResponse = {
  workflow_id: string;
  plan_id: string;
  status: 'queued' | 'running' | 'succeeded' | 'failed';
  step_count: number;
  next_step: number;
  error?: string;
  created_at: string;
};

