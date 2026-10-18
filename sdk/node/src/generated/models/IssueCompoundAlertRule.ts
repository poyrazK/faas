/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Named AND rule requiring event frequency and verified customer impact in one shared window.
 */
export type IssueCompoundAlertRule = {
  key: string;
  enabled: boolean;
  /**
   * Compound-rule environment selector; leave empty to evaluate every issue environment.
   */
  environment: string;
  minimum_events: number;
  minimum_customers: number;
  window_seconds: number;
  cooldown_seconds: number;
};

