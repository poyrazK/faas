/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteHealthHistoryEntry } from './RouteHealthHistoryEntry.js';
export type RouteHealthHistoryPage = {
  app_id: string;
  deployment_id: string;
  entries: Array<RouteHealthHistoryEntry>;
  /**
   * Last returned decision UUID when another page exists.
   */
  next_cursor?: string;
};

