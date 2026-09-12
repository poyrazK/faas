/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Normalized server-side filters echoed by a request telemetry page.
 */
export type DebugTelemetryListFilters = {
  deployment_id?: string;
  status?: number;
  cold_boot?: boolean;
  /**
   * Consumer UUID, or __anonymous__ for anonymous traffic.
   */
  consumer_id?: string;
  min_latency_ms?: number;
};

