/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded representative request for a historical dependency edge. Request identifiers resolve only to retained redacted debugger evidence.
 */
export type DebugDependencyImpactExemplar = {
  request_id: string;
  trace_id?: string;
  window: 'baseline' | 'current';
  received_at: string;
  duration_ms: number;
  http_status: number;
  error: boolean;
  count: number;
};

