/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Archive evidence for a native PostgreSQL point-in-time restore target.
 */
export type ManagedPostgresRestoreCheck = {
  point_in_time: string;
  outcome: 'covered' | 'no_base_backup' | 'wal_gap' | 'unverified';
  /**
   * Stable reason code for the archive check.
   */
  reason: string;
  backup_completed_at?: string;
  /**
   * Timestamp of the archived transaction record that would let PostgreSQL stop replay at the target.
   */
  wal_stop_at?: string;
  /**
   * WAL segment required by the selected restore path and missing from the archive.
   */
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

