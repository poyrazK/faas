/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionPipelineReusedStep } from './ExecutionPipelineReusedStep.js';
/**
 * Rerun receipt linking the replacement pipeline to its original job and reused outputs.
 */
export type ExecutionPipelineRerun = {
  parent_id: string;
  parent_workflow_id: string;
  parent_plan_id: string;
  reused: Array<ExecutionPipelineReusedStep>;
};

