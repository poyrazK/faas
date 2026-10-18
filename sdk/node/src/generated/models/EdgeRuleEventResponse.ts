/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One sampled request an edge rule matched (ADR-964), or a request a
 * kind=waf rule detected as a likely attack (ADR-831 amendment 5).
 * Detections carry the CRS rule IDs that scored, their attack
 * categories and the anomaly score, never the matched payload.
 *
 */
export type EdgeRuleEventResponse = {
  id: string;
  rule_id: string;
  /**
   * Empty once the rule is deleted.
   */
  rule_name?: string;
  rule_kind?: string;
  /**
   * matched (enforced rule) and logged (log-mode rule) are rule matches. detected is a kind=waf detection in a sampled request; warned and blocked are kind=waf detections in-path by a warn or block rule (blocked requests were answered with 403).
   */
  outcome: 'matched' | 'logged' | 'detected' | 'warned' | 'blocked';
  occurred_at: string;
  request_id?: string;
  method?: string;
  host?: string;
  /**
   * Request path without the query string.
   */
  path?: string;
  /**
   * Trusted client IP, when known.
   */
  client_ip?: string;
  country?: string;
  user_agent?: string;
  /**
   * CRS rule IDs that scored in a kind=waf detection.
   */
  waf_rule_ids?: Array<number>;
  /**
   * CRS attack categories of a kind=waf detection (sqli, xss, lfi, ...).
   */
  waf_categories?: Array<string>;
  /**
   * CRS inbound anomaly score of a kind=waf detection.
   */
  waf_score?: number;
};

