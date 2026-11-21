/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateManagedExecutionWorkflowRequest } from './CreateManagedExecutionWorkflowRequest.js';
/**
 * New workflow identity and acknowledgement of previously started steps that may repeat.
 */
export type RerunExecutionPipelineRequest = {
  workflow_id: string;
  original: CreateManagedExecutionWorkflowRequest;
  repeat_steps?: Array<string>;
};

