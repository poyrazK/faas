/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One collapsed telemetry row persisted by the recorder; count is the number of gateway-served requests represented by it.
 */
export type DebugTelemetryRequestItem = {
  id: string;
  deployment_id: string;
  /**
   * Route template (NOT expanded URL).
   */
  route: string;
  method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS';
  status: number;
  latency_ms: number;
  /** Number of requests represented by this collapsed telemetry row. */
  count: number;
  cold_boot: boolean;
  /**
   * W3C trace-id hex (32 chars), null when unset.
   */
  trace_id?: string | null;
  received_at: string;
};
