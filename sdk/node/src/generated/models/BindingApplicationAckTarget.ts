/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Authorized resident workload and managed secret metadata, with independently versioned reload and application observations. Contains no credential values, hashes or private binding IDs. Empty workload_name means the main workload.
 */
export type BindingApplicationAckTarget = {
  deployment_id: string;
  instance_id: string;
  workload_name?: string;
  runtime_state: string;
  key: string;
  reload_support: 'enabled' | 'disabled' | 'unknown';
  current_version: number;
  reload_version: number;
  projection?: 'updated' | 'unchanged' | 'failed';
  signal?: 'sent' | 'queued' | 'failed' | 'not_attempted';
  reload_at?: string;
  application_ack_version: number;
  application_ack?: 'applied' | 'failed';
  application_ack_at?: string;
  /**
   * Active execution identity; absent for legacy or retired processes.
   */
  process_generation?: string;
  /**
   * Execution identity supplied in the application ACK; strict adoption requires it to match process_generation.
   */
  application_ack_generation?: string;
  /**
   * Derived status for this workload and its secret projection and notification.
   */
  reload_status?: 'current' | 'failed' | 'stale' | 'unknown';
  /**
   * Stable sanitized reason for reload_status; contains no raw errors or secret data.
   */
  reload_reason?: 'current' | 'binding_secret_unexpected' | 'target_inconsistent' | 'reload_observation_missing' | 'reload_observation_time_invalid' | 'reload_version_invalid' | 'projection_failed' | 'signal_failed' | 'reload_outcome_unknown' | 'reload_stale';
  /**
   * Derived status for this workload and its application acknowledgement, including process-generation fencing.
   */
  application_ack_status?: 'current' | 'failed' | 'stale' | 'unknown';
  /**
   * Stable sanitized reason for application_ack_status; contains no generation values, raw errors or secret data.
   */
  application_ack_reason?: 'current' | 'binding_secret_unexpected' | 'process_generation_missing' | 'process_generation_invalid' | 'application_ack_generation_missing' | 'application_ack_generation_mismatch' | 'target_inconsistent' | 'reload_disabled' | 'reload_support_unknown' | 'application_ack_missing' | 'application_ack_time_invalid' | 'application_ack_version_invalid' | 'application_ack_outcome_unknown' | 'application_ack_stale' | 'application_ack_failed';
};

