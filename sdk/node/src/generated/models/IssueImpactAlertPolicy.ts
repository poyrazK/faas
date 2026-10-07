/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * App-level customer-impact threshold measured using verified distinct customers in a rolling 24-hour window.
 */
export type IssueImpactAlertPolicy = {
  enabled: boolean;
  minimum_customers: number;
  window_seconds: number;
};

