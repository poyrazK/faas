/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Direct submission history for one node/channel. Clocks and outcome
 * fields remain null or absent when unavailable. Evaluated_at describes
 * the recording evaluation; last_run_at is the source probe-completion
 * clock. Last_attempt_at and last_success_at are historical completed
 * submission clocks, not provider delivery or acknowledgement times.
 *
 */
export type ObsServingProbeNotification = {
  node: string;
  channel: 'heartbeat' | 'webhook';
  state: 'disabled' | 'unknown' | 'last_submission_succeeded' | 'last_submission_failed';
  reason?: 'query_failed' | 'recording_missing' | 'recording_not_current' | 'recording_unknown' | 'source_unavailable' | 'metadata_inconsistent';
  /**
   * Optional detail for a recording_unknown outcome from a coherent current evidence-status recording. Distinguishes valid enabled channels without history from missing or invalid telemetry and an unavailable source. Older rules omit this field. A current evaluation does not establish a submission or external receipt.
   */
  evidence_reason?: 'no_submission_yet' | 'telemetry_missing' | 'telemetry_invalid' | 'source_unavailable';
  enabled: boolean | null;
  evaluated_at: string | null;
  last_run_at: string | null;
  last_attempt_at: string | null;
  last_success_at: string | null;
  failure_code?: 'none' | 'dns' | 'connection' | 'tls' | 'timeout' | 'http_status' | 'unknown';
};

