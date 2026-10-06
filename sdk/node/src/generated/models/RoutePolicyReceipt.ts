/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RoutePolicyAppliedChange } from './RoutePolicyAppliedChange.js';
import type { RouteRequirementsReport } from './RouteRequirementsReport.js';
/**
 * Durable historical transaction outcome, verified configuration, and actual rule identifiers.
 */
export type RoutePolicyReceipt = {
  id: string;
  app_id: string;
  plan_sha256: string;
  applied_at: string;
  changes: Array<RoutePolicyAppliedChange>;
  verification: RouteRequirementsReport;
};

