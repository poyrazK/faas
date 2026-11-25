/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ManagedPostgresRecoveryStatus = {
  region: string;
  outcome: 'covered' | 'unverified' | 'no_base_backup' | 'wal_gap';
  reason: string;
  recovery_point?: string;
  recovery_lag_seconds?: number;
  missing_wal_segment?: string;
  /**
   * PostgreSQL timeline selected from the archive.
   */
  recovery_timeline?: number;
  /**
   * Timeline history file needed to select the recovery WAL.
   */
  missing_timeline_history?: string;
};

