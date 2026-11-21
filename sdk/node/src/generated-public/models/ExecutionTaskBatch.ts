/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionRetryPolicy } from './ExecutionRetryPolicy.js';
import type { ExecutionTaskBatchItem } from './ExecutionTaskBatchItem.js';
/**
 * Expands into independent steps for one immutable task version. Cannot be combined with steps. Item labels must be unique. Account and customer/task budgets still apply to every physical attempt.
 */
export type ExecutionTaskBatch = {
  name: string;
  version: number;
  customer_ref?: string;
  retry?: ExecutionRetryPolicy;
  items: Array<ExecutionTaskBatchItem>;
};

