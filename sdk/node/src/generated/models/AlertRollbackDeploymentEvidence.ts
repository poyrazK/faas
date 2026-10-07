/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact deployment request evidence for post-deploy rollback. Windows contain only complete minutes after cutover and before the fire with 30 seconds of ingestion lag. Requests counts only 2xx and 5xx responses, weighted by telemetry publisher counts. At least 20 requests, one server error, and a sample within two minutes of the window end are required. Only error_rate_pct with gt or gte comparisons qualifies. Unaccepted fires expire after two minutes. Accepted evidence is immutable and included in intent and completion audits; existing accepted operations continue without requalification.
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
  minimum_requests: number;
  error_rate_pct: number;
  status: 'pending' | 'breached' | 'healthy' | 'insufficient' | 'unavailable' | 'unsupported' | 'stale' | 'expired';
  code?: string;
};

