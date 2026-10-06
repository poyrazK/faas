/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationExecution } from './OperationExecution.js';
/**
 * Ascending bounded page of retained execution generations.
 */
export type OperationExecutionsResponse = {
  executions: Array<OperationExecution>;
  next_generation?: number;
};

