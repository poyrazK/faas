/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteCheckHistorySummary } from './RouteCheckHistorySummary.js';
/**
 * Deployment-scoped page of retained route check summaries and an optional continuation cursor.
 */
export type RouteCheckHistoryPage = {
  app_id: string;
  deployment_id: string;
  entries: Array<RouteCheckHistorySummary>;
  next_cursor?: string;
};

