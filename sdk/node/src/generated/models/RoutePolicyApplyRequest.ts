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
   * Apply using the saved app intent bound by the reviewed plan rather than an inline requirements document.
   */
  saved?: boolean;
  /**
   * Required saved intent revision from the reviewed plan when saved=true; a changed revision rejects the apply.
   */
  expected_revision?: number;
  requirements?: RouteRequirementsConfig;
  /**
   * App-owned captured deployment required for saved or version 2 apply; its contract must match the reviewed plan.
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

