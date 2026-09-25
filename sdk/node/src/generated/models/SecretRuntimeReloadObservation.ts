/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Non-sensitive guest-init projection/signal outcome for one secret version on one active runtime. Not an application acknowledgement.
 */
export type SecretRuntimeReloadObservation = {
  /**
   * Runtime instance ID that reported this outcome.
   */
  instance_id: string;
  /**
   * Secret version observed by guest-init; compare with delivery_version to detect stale status.
   */
  version: number;
  projection: 'updated' | 'unchanged' | 'failed';
  signal: 'sent' | 'queued' | 'failed' | 'not_attempted';
  observed_at: string;
  error_code?: 'projection_failed' | 'signal_failed';
};

