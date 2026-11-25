/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresRecoveryStatus } from './ManagedPostgresRecoveryStatus.js';
import type { ManagedPostgresRestoreDrillStatus } from './ManagedPostgresRestoreDrillStatus.js';
/**
 * Configured point-in-time restore retention and recorded backup and restore evidence for a native PostgreSQL database.
 */
export type ManagedPostgresBackupStatus = {
  recovery?: ManagedPostgresRecoveryStatus;
  /**
   * Configured retention in seconds. Zero disables the restore window.
   */
  restore_window_seconds: number;
  /**
   * When the scheduler last recorded a successful base backup.
   */
  last_base_backup_at?: string;
  latest_restore_drill?: ManagedPostgresRestoreDrillStatus;
  /**
   * The most recent successful drill for this database. Verification applies to its specific target.
   */
  last_verified_restore?: ManagedPostgresRestoreDrillStatus;
};

