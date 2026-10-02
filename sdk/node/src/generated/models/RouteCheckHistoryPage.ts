/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteCheckHistorySummary } from './RouteCheckHistorySummary.js';
export type RouteCheckHistoryPage = {
  app_id: string;
  deployment_id: string;
  entries: Array<RouteCheckHistorySummary>;
  next_cursor?: string;
};

