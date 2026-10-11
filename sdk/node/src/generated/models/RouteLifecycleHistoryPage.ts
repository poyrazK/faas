/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteLifecycleHistoryEntry } from './RouteLifecycleHistoryEntry.js';
/**
 * Newest retained lifecycle reviews with an optional continuation cursor.
 */
export type RouteLifecycleHistoryPage = {
  app_id: string;
  entries: Array<RouteLifecycleHistoryEntry>;
  next_cursor?: string;
};

