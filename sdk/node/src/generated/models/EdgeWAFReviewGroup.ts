/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One edge rule's detections by one CRS rule on one path.
 */
export type EdgeWAFReviewGroup = {
  rule_id: string;
  rule_name?: string;
  rule_mode?: 'observe' | 'warn' | 'block';
  crs_rule_id: number;
  /**
   * CRS attack category of the rule (sqli, xss, rce, lfi, rfi, php, java, generic, protocol, multipart, scanner, session_fixation, other).
   */
  category: string;
  /**
   * Request path with id-like segments (numbers, UUIDs, long hex or tokens) read as {id}.
   */
  path: string;
  events: number;
  /**
   * Detections in sampled requests.
   */
  detected: number;
  warned: number;
  blocked: number;
  /**
   * Distinct client IPs.
   */
  clients: number;
  /**
   * Share of the group's detections from its busiest client.
   */
  top_client_percent: number;
  /**
   * Share of the group's detections from clients flagged on 5 or more paths in the window.
   */
  scanner_percent: number;
  first_seen: string;
  last_seen: string;
  /**
   * The newest example request IDs; look them up with the events listing.
   */
  request_ids: Array<string>;
  /**
   * The documented legitimate traffic this CRS rule flags.
   */
  known_false_positive?: string;
  /**
   * The rule excludes this CRS rule now.
   */
  excluded: boolean;
  assessment: 'likely_false_positive' | 'likely_attack' | 'review' | 'excluded';
  /**
   * Why the group got its assessment.
   */
  reason: string;
};

