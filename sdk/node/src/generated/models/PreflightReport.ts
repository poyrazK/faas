/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PreflightPlanBudget } from './PreflightPlanBudget.js';
import type { PreflightSource } from './PreflightSource.js';
import type { PreflightVerdict } from './PreflightVerdict.js';
/**
 * One complete preflight answer, pinned to the commit it was computed
 * from so a permalink always re-renders the same verdict.
 *
 */
export type PreflightReport = {
  source: PreflightSource;
  /**
   * The commit the verdict was computed from.
   */
  commit_sha: string;
  verdict: PreflightVerdict;
  plan_budgets: Array<PreflightPlanBudget>;
  checked_at: string;
};

