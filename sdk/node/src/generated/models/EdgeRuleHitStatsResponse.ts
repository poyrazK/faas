/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One rule's match counts over the window (ADR-904).
 */
export type EdgeRuleHitStatsResponse = {
  rule_id: string;
  /**
   * Requests an enforced rule matched.
   */
  matched: number;
  /**
   * Requests a log-mode rule matched.
   */
  logged: number;
};

