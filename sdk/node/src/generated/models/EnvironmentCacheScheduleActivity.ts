/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentCacheScheduleEvent } from './EnvironmentCacheScheduleEvent.js';
/**
 * Exact environment activity page with an account-bound checkpoint that can be reused after reaching the current tail.
 */
export type EnvironmentCacheScheduleActivity = {
  app_id: string;
  project_id: string;
  environment_id: string;
  scope: string;
  entries: Array<EnvironmentCacheScheduleEvent>;
  cursor: string;
  has_more: boolean;
};

