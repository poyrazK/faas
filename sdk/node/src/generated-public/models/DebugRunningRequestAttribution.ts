/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The nearest retained request-telemetry representative linked to a
 * request-activity cause. A representative may contain several
 * collapsed requests; match_delta_ms and count make that limitation
 * explicit.
 *
 */
export type DebugRunningRequestAttribution = {
  telemetry_id: string;
  deployment_id: string;
  route: string;
  method: string;
  trace_id?: string;
  received_at: string;
  count: number;
  wake_id?: string;
  instance_id?: string;
  match_delta_ms: number;
};

