/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One rule's match counts over the window.
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
  /**
   * With series=true, matched plus logged per hour; the first value is the hour starting at since, the last the current hour.
   */
  series?: Array<number>;
};

