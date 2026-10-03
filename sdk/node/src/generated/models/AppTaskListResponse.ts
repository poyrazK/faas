/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppTaskResponse } from './AppTaskResponse.js';
/**
 * App-scoped page of task receipts. `next_offset` is -1 when there is no
 * following page; otherwise pass it as `offset`.
 *
 */
export type AppTaskListResponse = {
  tasks: Array<AppTaskResponse>;
  limit: number;
  offset: number;
  /**
   * Offset for the following app-task page, or -1 when this page is final.
   */
  next_offset: number;
};

