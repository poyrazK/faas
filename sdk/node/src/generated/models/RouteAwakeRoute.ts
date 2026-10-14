/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteAwakeSuggestion } from './RouteAwakeSuggestion.js';
import type { RouteAwakeWhatIf } from './RouteAwakeWhatIf.js';
/**
 * One route's share of the app's awake time and what removing its requests would change.
 */
export type RouteAwakeRoute = {
  method: string;
  /**
   * Route template, for example /orders/{id}.
   */
  route: string;
  /**
   * Requests that restarted the idle timer.
   */
  requests: number;
  /**
   * Cold boots this route's requests triggered.
   */
  wakes: number;
  awake_seconds: number;
  awake_share: number;
  gb_hours: number;
  pattern: 'periodic' | 'irregular';
  /**
   * Approximate interval when pattern is periodic.
   */
  interval_seconds?: number;
  if_removed: RouteAwakeWhatIf;
  suggestion?: RouteAwakeSuggestion;
};

