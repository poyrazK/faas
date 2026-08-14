/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { JobTaskResponse } from './JobTaskResponse.js';
/**
 * Page of tasks for one run. Tasks are ordered ASCENDING
 * by `task_index` (the dispatch order). Cursor uses
 * `?before=<task_index>`.
 *
 */
export type ListRunTasksResponse = {
  tasks: Array<JobTaskResponse>;
  /**
   * Cursor — return tasks with task_index > before.
   */
  next_before?: number | null;
};

