/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentCacheSchedule } from './EnvironmentCacheSchedule.js';
/**
 * Scoped live schedule page ordered by execution timestamp and schedule UUID, with filter-bound continuation.
 */
export type EnvironmentCacheScheduleList = {
  app_id: string;
  project_id: string;
  environment_id: string;
  scope: string;
  entries: Array<EnvironmentCacheSchedule>;
  next_cursor?: string;
};

