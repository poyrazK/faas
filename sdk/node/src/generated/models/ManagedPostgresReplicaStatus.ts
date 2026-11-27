/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Standby health sampled without waking a native production database.
 */
export type ManagedPostgresReplicaStatus = {
  /**
   * Whether the standby was available for reads when observed.
   */
  available: boolean;
  /**
   * WAL backlog between primary flush and standby replay. Omitted when unknown.
   */
  replay_lag_bytes?: number;
  /**
   * When the replay evidence was collected.
   */
  observed_at?: string;
  /**
   * Age in milliseconds when the response was built.
   */
  sample_age_ms?: number;
};

