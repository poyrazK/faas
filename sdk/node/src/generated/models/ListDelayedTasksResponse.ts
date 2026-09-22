/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DelayedTaskResponse } from './DelayedTaskResponse.js';
/**
 * One newest-first page of delayed tasks for an app.
 */
export type ListDelayedTasksResponse = {
  tasks: Array<DelayedTaskResponse>;
  next_before?: string;
};

