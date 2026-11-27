/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionArtifactInput } from './ExecutionArtifactInput.js';
import type { ExecutionFileInput } from './ExecutionFileInput.js';
/**
 * Task selection, invocation input and optional file or completion configuration.
 */
export type InvokeExecutionTaskRequest = {
  customer_ref?: string;
  name: string;
  version: number;
  input?: any;
  input_files?: Array<ExecutionFileInput>;
  artifact_inputs?: Array<ExecutionArtifactInput>;
  workflow_id?: string;
  step_label?: string;
};

