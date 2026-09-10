/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One bounded latency-bucket row representing gateway-served requests, persisted by the recorder/publisher.
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
  /**
   * Inclusive upper bound of the bounded latency bucket represented by this row.
   */
  latency_ms: number;
  /**
   * Number of original requests represented by this collapsed telemetry row.
   */
  count: number;
  cold_boot: boolean;
  /**
   * W3C trace-id hex (32 chars), null when unset.
   */
  trace_id?: string | null;
  received_at: string;
  /**
   * Opaque wake identifier when this request admitted a wake; omitted for warm requests.
   */
  wake_id?: string;
  /**
   * Opaque instance identifier that served the request; omitted when no target was reached.
   */
  instance_id?: string;
};

