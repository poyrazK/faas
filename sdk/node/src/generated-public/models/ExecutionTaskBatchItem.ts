/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionFileInput } from './ExecutionFileInput.js';
/**
 * A labeled invocation input submitted as part of a task batch.
 */
export type ExecutionTaskBatchItem = {
  label: string;
  /**
   * JSON input for this batch item.
   */
  input?: any;
  input_files?: Array<ExecutionFileInput>;
};

