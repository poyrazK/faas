/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteInsight } from './RouteInsight.js';
/**
 * An app's active route insights, then those resolved in the last seven days.
 */
export type RouteInsightsResponse = {
  slug: string;
  insights: Array<RouteInsight>;
};

