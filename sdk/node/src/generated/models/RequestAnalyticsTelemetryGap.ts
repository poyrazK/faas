/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One reason request analytics may be missing requests: a gateway with no or a stale delivery report
 * (`telemetry_coverage_missing`, `telemetry_coverage_stale`), telemetry switched off or sampled
 * (`telemetry_disabled`, `telemetry_sampled`), requests still waiting to be delivered
 * (`telemetry_ingestion_pending`), or requests lost or a gateway restarted during the window (`telemetry_window_incomplete`).
 *
 */
export type RequestAnalyticsTelemetryGap = 'telemetry_coverage_missing' | 'telemetry_coverage_stale' | 'telemetry_disabled' | 'telemetry_sampled' | 'telemetry_ingestion_pending' | 'telemetry_window_incomplete';
