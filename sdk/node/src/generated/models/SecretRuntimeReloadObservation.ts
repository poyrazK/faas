/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Non-sensitive active runtime target and optional guest-init projection/signal outcome plus application-owned reload acknowledgement. An application acknowledgement is a self-attestation, not independent verification.
 */
export type SecretRuntimeReloadObservation = {
  /**
   * Authorized active runtime instance ID.
   */
  instance_id: string;
  /**
   * Current active instance state.
   */
  runtime_state: 'waking' | 'cold_booting' | 'running' | 'draining' | 'snapshotting' | 'migrating' | 'warm';
  /**
   * Whether this deployment can participate in live reload; unknown means its image opt-in predates persisted metadata.
   */
  reload_support: 'enabled' | 'disabled' | 'unknown';
  /**
   * Whether this runtime has reported a guest-init outcome. False is unknown, never success.
   */
  reported: boolean;
  /**
   * Secret version observed by guest-init; compare with delivery_version to detect stale status. Present only when reported is true.
   */
  version?: number;
  projection?: 'updated' | 'unchanged' | 'failed';
  signal?: 'sent' | 'queued' | 'failed' | 'not_attempted';
  observed_at?: string;
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

