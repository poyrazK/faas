/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Configured poll and in-flight allowances with the existing 60-second scrape/evaluation margin. Completed iterations include idle/error returns. Initialization is the origin before any completion. Intentional drain never establishes resumed work.
 */
export type ObsRuntimeBuilderWorkerValues = {
  enabled: boolean;
  inflight: boolean;
  started_at: string;
  last_attempt_at?: string;
  last_completed_at?: string;
  interval_seconds: number;
  busy_allowance_seconds: number;
  progress_age_seconds: number;
  progress_allowance_seconds: number;
};

