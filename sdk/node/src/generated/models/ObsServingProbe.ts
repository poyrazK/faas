/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One expected node/URL pair. Raw URL bytes remain private; url_id fingerprints exact URL bytes, including query parameters. Evaluation time describes the recording, and last_run_at is the probe loop clock.
 */
export type ObsServingProbe = {
  node: string;
  url_id: string;
  state: 'healthy' | 'missing' | 'stale' | 'invalid_telemetry' | 'content_disabled' | 'budget_mismatch' | 'failing' | 'unknown';
  reason?: string;
  reporting: boolean | null;
  evaluated_at: string | null;
  last_run_at: string | null;
  expected_first_byte_seconds: number;
  max_result_age_seconds: number;
};

