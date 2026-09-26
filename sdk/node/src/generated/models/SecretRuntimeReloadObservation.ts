/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Non-sensitive guest-init projection/signal outcome and optional application-owned reload acknowledgement for one runtime. An application acknowledgement is a self-attestation, not independent verification.
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
  /**
   * Secret version the application claims to have applied; compare with delivery_version, independently of the guest-init report version.
   */
  application_ack_version?: number;
  /**
   * Explicit application-owned outcome after rereading and applying FAAS_SECRETS_FILE. Missing means unknown, not failed.
   */
  application_ack?: 'applied' | 'failed';
  application_ack_at?: string;
  /**
   * Closed, non-sensitive application-reported failure code.
   */
  application_ack_error_code?: 'application_reload_failed';
};

