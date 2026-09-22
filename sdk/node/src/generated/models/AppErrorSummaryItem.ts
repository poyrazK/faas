/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One row of the errors summary (ADR-096 / PR-B).
 */
export type AppErrorSummaryItem = {
  /**
   * 64-hex-char SHA-256 fingerprint identifying this error group; stable across requests with the same (route, status, error_class).
   */
  fingerprint: string;
  /**
   * Closed-vocabulary error class. 'unhandled' is collapsed under 'Other' in the UI; the DB stores the precise class.
   */
  error_class: 'db_timeout' | 'stripe_timeout' | 'null_pointer' | 'invalid_json' | 'wake_failed' | 'upstream_5xx' | 'unhandled' | 'client_error';
  /**
   * Matched route template (e.g. `/users/{id}`), NEVER the expanded URL.
   */
  route: string;
  http_status: number;
  /**
   * Issue count (deduped within AppErrorsDedupeWindowSeconds=3600).
   */
  count: number;
  /**
   * Total distinct request rows for this fingerprint.
   */
  request_count: number;
  first_seen_at: string;
  last_seen_at: string;
  /**
   * PII-redacted sample message (already-redacted at write time; ≤AppErrorsSampleMessageCapBytes=512 bytes).
   */
  sample_message: string;
  /**
   * Most recent platform instance that produced this fingerprint.
   */
  last_instance_id?: string;
  /**
   * Most recent compute node that produced this fingerprint.
   */
  last_node_id?: string;
  last_region?: string;
  last_commit_sha?: string;
  last_deployment_tag?: string;
  last_deployment_created_at?: string;
  last_image_digest?: string;
};

