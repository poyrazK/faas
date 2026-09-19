/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One redacted span on the selected causal path. Exclusive time excludes overlapping direct children.
 */
export type DebugCriticalPathSpan = {
  span_id: string;
  parent_span_id?: string;
  name: string;
  kind: string;
  dependency_type?: 'managed_binding' | 'outbound_integration' | 'guest_transport' | 'platform_internal';
  dependency_kind?: string;
  status?: string;
  start_time: string;
  end_time: string;
  duration_ms: number;
  /**
   * Wall time not covered by overlapping direct child spans.
   */
  exclusive_ms: number;
};

