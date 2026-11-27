/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Latest archive evidence and recovery point for a configured destination region.
 */
export type ManagedPostgresRecoveryStatus = {
  region: string;
  outcome: 'covered' | 'unverified' | 'no_base_backup' | 'wal_gap';
  reason: string;
  recovery_point?: string;
  recovery_lag_seconds?: number;
  missing_wal_segment?: string;
  /**
   * PostgreSQL timeline supporting the verified point in the recovery region.
   */
  recovery_timeline?: number;
  /**
   * History file the destination archive needs to verify the promoted lineage.
   */
  missing_timeline_history?: string;
};

