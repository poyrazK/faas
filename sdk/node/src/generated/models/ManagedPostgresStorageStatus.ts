/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresAutoGrowStatus } from './ManagedPostgresAutoGrowStatus.js';
import type { ManagedPostgresStorageAlert } from './ManagedPostgresStorageAlert.js';
import type { ManagedPostgresVolumeStatus } from './ManagedPostgresVolumeStatus.js';
/**
 * Storage allocation and latest filesystem observations for a native PostgreSQL database.
 */
export type ManagedPostgresStorageStatus = {
  /**
   * Committed allocation per volume in bytes.
   */
  allocated_bytes: number;
  /**
   * Whether the database is configured to have a standby.
   */
  standby_expected: boolean;
  primary?: ManagedPostgresVolumeStatus;
  standby?: ManagedPostgresVolumeStatus;
  auto_grow?: ManagedPostgresAutoGrowStatus;
  /**
   * Latest 10 recorded primary storage alert transitions, newest first.
   */
  alerts?: Array<ManagedPostgresStorageAlert>;
};

