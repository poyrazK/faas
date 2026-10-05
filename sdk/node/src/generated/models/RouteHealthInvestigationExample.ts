/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Metadata for one retained matching telemetry row. Its weight may exceed one request; a trace ID is a link rather than a promise of retained spans.
 */
export type RouteHealthInvestigationExample = {
  telemetry_id: string;
  received_at: string;
  status: number;
  latency_ms: number;
  represented_requests: number;
  trace_id?: string;
  /**
   * Authenticated app debugger evidence path for this telemetry row. Retention and authorization are checked again when followed.
   */
  evidence_path: string;
};

