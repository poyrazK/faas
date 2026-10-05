/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppHealthFinding } from './AppHealthFinding.js';
/**
 * Safe evidence explanation and an optional inspection destination.
 */
export type AppHealthCheck = {
  code: 'workload' | 'deployment' | 'latest_deployment' | 'readiness' | 'traffic_readiness' | 'maintenance' | 'requests' | 'collection';
  status: 'pass' | 'warning' | 'fail' | 'unknown' | 'not_applicable';
  /**
   * Safe explanation without raw backend errors or probe output.
   */
  detail: string;
  /**
   * Stable diagnostic reason, when available.
   */
  reason?: 'collection_gap' | 'capacity_below_target' | 'request_plan_restricted' | 'request_scope_unavailable' | 'request_evidence_unavailable' | 'request_coverage_incomplete' | 'request_evidence_stale' | 'request_evidence_invalid' | 'requests_unexercised' | 'request_volume_insufficient' | 'request_error_rate_severe' | 'request_error_rate_elevated' | 'request_errors_below_threshold' | 'requests_observed';
  action?: 'deployments' | 'configuration' | 'logs' | 'metrics' | 'errors';
  deployment_id?: string;
  /**
   * Independent replica readiness failures and missing evidence, with failures first and stable target order within severity.
   */
  findings?: Array<AppHealthFinding>;
  /**
   * Additional findings were omitted; capacity counts still cover the complete bounded instance scan.
   */
  findings_truncated?: boolean;
};

