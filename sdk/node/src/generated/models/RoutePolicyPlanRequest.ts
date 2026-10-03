/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteRequirementsConfig } from './RouteRequirementsConfig.js';
/**
 * Supply exactly one source: inline requirements or saved=true. Saved mode reads current app intent in the same snapshot as policy and capture; deployment_id is required. expected_revision optionally pins saved planning. Explicit burst choice is required for creating throttles without a selected policy.
 */
export type RoutePolicyPlanRequest = {
  /**
   * Use current saved app requirements instead of inline requirements.
   */
  saved?: boolean;
  /**
   * Saved mode only; optional for planning and mandatory for applying the reviewed saved plan.
   */
  expected_revision?: number;
  requirements?: RouteRequirementsConfig;
  /**
   * Required for saved or inline version 2 requirements; captured deployment must belong to the app.
   */
  deployment_id?: string;
  throttle_burst?: number;
  /**
   * Version 2 requirements only. Propose compatible budgets within declared group prefixes; explicitly permits uncaptured and future path scope. Throttles remain separate.
   */
  consolidate_budgets?: boolean;
};

