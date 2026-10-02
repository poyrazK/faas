/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteRequirementsConfig } from './RouteRequirementsConfig.js';
/**
 * Supply exactly one source: inline requirements or saved=true. Saved apply requires expected_revision from the reviewed plan and rejects any saved intent revision change. The reviewed fingerprint binds its content hash, capture, options and configuration; patch bodies are recomputed under transaction locks. Identical committed retries return the original receipt even after saved intent changes.
 */
export type RoutePolicyApplyRequest = {
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
   * Must match the reviewed group plan; synthesis is recomputed under transaction locks.
   */
  consolidate_budgets?: boolean;
  expected_plan_sha256: string;
  confirm: boolean;
};

