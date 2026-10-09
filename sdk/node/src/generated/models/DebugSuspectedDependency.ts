/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The classified dependency whose p95 regressed most between the previous and the regressed deployment on this route (ADR-829). Bounded and redacted; also sent in debug.regression.* webhooks.
 */
export type DebugSuspectedDependency = {
  type: 'managed_binding' | 'outbound_integration' | 'guest_transport' | 'platform_internal' | 'app_dependency';
  kind?: string;
  name: string;
  p95_base_ms: number;
  p95_ms: number;
  regression_factor: number;
};

