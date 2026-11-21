/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionPipelineCompletion } from './ExecutionPipelineCompletion.js';
/**
 * App-scoped RAP pipeline receipt. Completion is absent while the pipeline is pending. Reads require the owning endpoint and a live original thread grant.
 */
export type ExecutionPipelineReceipt = {
  id: string;
  completion?: ExecutionPipelineCompletion;
};

