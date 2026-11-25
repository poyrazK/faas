/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresMigrationPreparation } from './ManagedPostgresMigrationPreparation.js';
import type { ManagedPostgresMigrationVerification } from './ManagedPostgresMigrationVerification.js';
/**
 * Latest observed progress for the native logical subscriber.
 */
export type ManagedPostgresMigrationProgress = {
  operation_id?: string;
  state: 'none' | 'preparing' | 'copying' | 'catching_up' | 'live' | 'paused' | 'resync_required' | 'stopped' | 'activated';
  reason?: string;
  tables: number;
  tables_ready: number;
  /**
   * Observed source WAL bytes beyond the subscriber's confirmed position.
   */
  apply_lag_bytes: number;
  /**
   * Observed source WAL retained by the main slot.
   */
  retained_wal_bytes: number;
  preparation?: ManagedPostgresMigrationPreparation;
  verification?: ManagedPostgresMigrationVerification;
};

