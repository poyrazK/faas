/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugGuestExecutionEvidence } from './DebugGuestExecutionEvidence.js';
/**
 * One bounded latency-bucket row representing gateway-served requests, persisted by the recorder/publisher.
 */
export type DebugTelemetryRequestItem = {
  /**
   * Internal telemetry row UUID. Use trace_id as the customer-visible request identifier when present.
   */
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
   * Customer-visible x-faas-request-id and W3C trace-id (32 hex chars), null for legacy rows.
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
  /**
   * Stable API consumer identity; omitted for anonymous or legacy traffic.
   */
  consumer_id?: string;
  /**
   * Compute node that served the request.
   */
  node_id?: string;
  /**
   * Region associated with the deployment target.
   */
  region?: string;
  /**
   * Source revision associated with the deployment.
   */
  commit_sha?: string;
  /**
   * Deployment tag associated with the request.
   */
  deployment_tag?: string;
  /**
   * Deployment creation timestamp supplied by the platform.
   */
  deployment_created_at?: string;
  /**
   * Immutable image/artifact digest associated with the deployment.
   */
  image_digest?: string;
  guest?: DebugGuestExecutionEvidence;
};

