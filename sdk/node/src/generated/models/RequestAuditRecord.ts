/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One non-collapsed completed request observed at the gateway.
 */
export type RequestAuditRecord = {
  event_id: string;
  account_id: string;
  app_id: string;
  consumer_id?: string;
  platform_tenant_id?: string;
  route_template: string;
  method: string;
  http_status: number;
  latency_ms: number;
  trace_id?: string;
  deployment_id?: string;
  commit_sha?: string;
  occurred_at: string;
  request_id?: string;
  /**
   * Verified public-gateway client IP when available.
   */
  source_ip?: string;
};

