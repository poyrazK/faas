/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Verification of configured 5xx budgets using production requests after an exact route generation was applied.
 */
export type ProjectRollbackLiveTraffic = {
  /**
   * One of pending, awaiting_gateways, awaiting_traffic, insufficient_traffic, evidence_unavailable, passed, or failed.
   */
  status: string;
  reason?: string;
  deadline?: string;
  /**
   * Earliest observation time requested when rechecking after resume.
   */
  observe_after?: string;
  checked_at?: string;
  route_generation?: number;
};

