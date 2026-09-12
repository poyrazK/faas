/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionResponse } from './ExecutionResponse.js';
/**
 * Account-scoped page of disposable execution receipts. `next_offset`
 * is -1 when there is no following page; otherwise pass it as `offset`.
 *
 */
export type ExecutionListResponse = {
  executions: Array<ExecutionResponse>;
  limit: number;
  offset: number;
  /**
   * Next offset, or -1 at the end of the result set.
   */
  next_offset: number;
};

