/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TraceSpan } from './TraceSpan.js';
/**
 * A single OpenTelemetry trace — the span tree for one
 * request (one wake). The shape mirrors the SDK's ReadOnlySpan
 * flattened to JSON: trace_id + span_id are W3C hex, attributes
 * are a string map (the operator's debug session is a grep, not
 * a query). The same trace_id may host a
 * `gateway.handler` → `gateway.route` → `sched.wake` →
 * `vmmd.create_*` → `guest.resume` → `guest.readiness` chain
 * (acceptance #1).
 *
 */
export type Trace = {
  trace_id: string;
  started_at?: string;
  last_seen_at?: string;
  spans: Array<TraceSpan>;
};

