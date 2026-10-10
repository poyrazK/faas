/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteAdviceSuggestion } from './RouteAdviceSuggestion.js';
/**
 * Route advisor suggestions derived from retained request telemetry (ADR-940). Nothing is applied.
 */
export type RouteAdviceResponse = {
  slug: string;
  from: string;
  until: string;
  /**
   * The requested window was shortened to plan retention.
   */
  window_clamped?: boolean;
  cache_max_age_seconds: number;
  routes_analyzed: number;
  suggestions: Array<RouteAdviceSuggestion>;
};

