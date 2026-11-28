/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact deployment request evidence for post-deploy rollback. Windows contain only complete minutes after cutover and before the fire with 30 seconds of ingestion lag. Requests counts only 2xx and 5xx responses, weighted by telemetry publisher counts. At least 20 requests, one server error, and a sample within two minutes of the window end are required. Only error_rate_pct or boot_failures with gt or gte comparisons qualifies. A boot_failures rule is judged on the deployment's own boots from cutover (or the rule window, if later) to the fire. A failed VM boot counts only on a node that completed a boot of another deployment in the window. Those failures must meet the rule and the deployment must have no completed boot or restore in that window; requests, server_errors, minimum_requests and error_rate_pct are then 0. Boot evidence is PostgreSQL-only, and a boot_failures rule never rolls back an in-flight canary or service rollout. Unaccepted fires expire after two minutes. Accepted evidence is immutable and included in intent and completion audits; existing accepted operations continue without requalification.
 */
export type AlertRollbackDeploymentEvidence = {
  version: 1;
  deployment_id: string;
  metric: string;
  comparison: string;
  threshold: number;
  window_spec: string;
  cutover_at: string;
  window_start: string;
  window_end: string;
  checked_at?: string;
  last_sample_at?: string;
  requests: number;
  server_errors: number;
  /**
   * 20 for error_rate_pct evidence; 0 for boot_failures evidence.
   */
  minimum_requests: number;
  error_rate_pct: number;
  /**
   * The deployment's failed VM boots in the window on nodes that started another deployment in it (boot_failures evidence only).
   */
  boot_failures?: number;
  /**
   * The deployment's completed boots or restores in the window (boot_failures evidence only).
   */
  boot_successes?: number;
  status: 'pending' | 'breached' | 'healthy' | 'insufficient' | 'unavailable' | 'unsupported' | 'stale' | 'expired';
  code?: string;
};

