/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded latency aggregate for one retained dependency span group. Total duration may include overlapping child spans and is not a critical-path sum.
 */
export type DebugRequestDependencyLatency = {
  type: 'application' | 'managed_binding' | 'outbound_integration' | 'guest_transport' | 'platform_internal';
  kind?: string;
  name: string;
  calls: number;
  errors?: number;
  total_duration_ms: number;
  max_duration_ms: number;
};

