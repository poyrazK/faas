/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteAdvicePolicyContext } from './RouteAdvicePolicyContext.js';
import type { RouteAdviceSuggestion } from './RouteAdviceSuggestion.js';
/**
 * Route advisor suggestions derived from retained request telemetry (ADR-976). Nothing is applied.
 */
export type RouteAdviceResponse = {
  policy_context?: RouteAdvicePolicyContext;
  /**
   * Advice report: App identity for explicitly scoped reports.
   */
  app_id?: string;
  /**
   * Advice report: Selected default or named environment; absent on aggregate reports.
   */
  scope?: string;
  /**
   * Advice report: Immutable project identity for named reports.
   */
  project_id?: string;
  /**
   * Advice report: Immutable named environment identity.
   */
  environment_id?: string;
  /**
   * Advice report: Only retained, identity-matched deployment history is included; absent for aggregate reports.
   */
  coverage?: 'retained_deployment_history';
  /**
   * Advice report: True for scoped reports: unattributed history and telemetry gaps remain unknown.
   */
  partial?: boolean;
  /**
   * Scoped learned evidence excludes legacy comparisons that may cross environments.
   */
  cacheability_coverage?: 'deployment_isolated_observations_only';
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

