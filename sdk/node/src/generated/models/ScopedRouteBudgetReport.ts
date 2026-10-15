/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorReport } from './RouteMonitorReport.js';
/**
 * Read-only evaluation of inherited or overridden budgets against one selected environment serving release. Completed current windows only; no pooled history, customer cohorts, scoped incidents or automatic rollback.
 */
export type ScopedRouteBudgetReport = {
  scope: string;
  project_id?: string;
  environment_id?: string;
  budget_source: 'app_policy' | 'environment_policy';
  budget_revision?: number;
  app_budget_revision?: number;
  report: RouteMonitorReport;
};

