/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Inspects matched requests with the OWASP Core Rule Set (ADR-831).
 * Every mode samples requests (bodies included) off the request path
 * and reports detections without blocking them. warn and block also
 * check headers and URL in-path with a smaller rule set at paranoia
 * level 1: warn tags a detected request's response with X-WAF-Warning,
 * block answers it with 403. Request bodies are never blocked. When the
 * app's in-path budget is exhausted a request passes unchecked (counted
 * as inline_skipped). Detections appear in the edge-protection summary
 * and the edge_waf_detections alert preset. Pro and above. Omitted
 * values are stored as their defaults.
 *
 */
export type EdgeRuleWAFAction = {
  /**
   * warn and block require paranoia_level 1.
   */
  mode?: 'observe' | 'warn' | 'block';
  /**
   * CRS paranoia level. Level 2 detects more and produces more false positives.
   */
  paranoia_level?: number;
  /**
   * Inbound anomaly score at which a request counts as a detection (one critical match scores 5).
   */
  anomaly_threshold?: number;
  /**
   * CRS rule IDs left out of scoring on this route, for known false positives.
   */
  exclude_rule_ids?: Array<number>;
  /**
   * Request body prefix inspected. Larger and streaming bodies are inspected up to this size only; larger values use more of the app's inspection budget.
   */
  inspect_body_bytes?: number;
};

