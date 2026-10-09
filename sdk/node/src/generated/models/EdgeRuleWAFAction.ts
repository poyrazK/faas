/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Inspects matched requests with the OWASP Core Rule Set (ADR-831 step
 * 1). Observe-only: requests are scored off the request path and never
 * blocked; detections appear in the edge-protection summary and the
 * edge_waf_detections alert preset. Pro and above. Omitted values are
 * stored as their defaults.
 *
 */
export type EdgeRuleWAFAction = {
  /**
   * Only observe is available; blocking modes need a later ADR-831 step.
   */
  mode?: 'observe';
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

