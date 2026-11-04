/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryReportTotals } from './WorkflowRecoveryReportTotals.js';
/**
 * Recovery counts for one workflow association and policy configuration version.
 */
export type WorkflowRecoveryReportGroup = {
  workflow: string;
  policy_version: number;
  totals: WorkflowRecoveryReportTotals;
};

