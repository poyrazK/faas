/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionTaskVersionResponse } from './ExecutionTaskVersionResponse.js';
/**
 * Page of visible task versions and its continuation token.
 */
export type ExecutionTaskListResponse = {
  tasks: Array<ExecutionTaskVersionResponse>;
  next_cursor?: string;
};

