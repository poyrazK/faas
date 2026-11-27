/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Recorded evidence from a single restore drill for this database.
 */
export type ManagedPostgresRestoreDrillStatus = {
  /**
   * ID of a requested drill.
   */
  drill_id?: string;
  outcome: 'queued' | 'running' | 'succeeded' | 'failed' | 'skipped';
  started_at: string;
  /**
   * Recovery point after the source commits the marker.
   */
  target_at?: string;
  finished_at?: string;
  /**
   * The last replayed commit reported by PostgreSQL.
   */
  recovered_to?: string;
  /**
   * Stable reason when a drill could not verify recovery.
   */
  error_code?: string;
  /**
   * Total time from admission through cleanup.
   */
  duration_seconds?: number;
};

