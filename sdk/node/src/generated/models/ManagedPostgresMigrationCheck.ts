/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Observed PostgreSQL prerequisites for a logical migration. A compatible result covers these specific checks.
 */
export type ManagedPostgresMigrationCheck = {
  outcome: 'compatible' | 'blocked' | 'unverified';
  reasons?: Array<string>;
  source_major?: number;
  target_major?: number;
  table_count: number;
  /**
   * Sequences that need synchronization before opening a replicated target to writes.
   */
  sequence_count: number;
  unsupported_table_count?: number;
  unsupported_tables?: Array<string>;
  source_bytes?: number;
  target_free_bytes?: number;
  /**
   * A negative value indicates unlimited WAL retention.
   */
  source_wal_limit_bytes: number;
  free_source_slots: number;
  free_source_senders: number;
};

