/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Evidence from a PostgreSQL major upgrade run on a temporary copy.
 */
export type ManagedPostgresUpgradeRehearsal = {
  database_id: string;
  rehearsal_id: string;
  request_id: string;
  source_major: number;
  target_major: number;
  outcome: 'queued' | 'running' | 'cleaning_up' | 'succeeded' | 'failed';
  /**
   * The active phase.
   */
  phase?: 'restore' | 'mark' | 'upgrade' | 'verify' | 'cleanup';
  /**
   * The phase that failed.
   */
  failed_phase?: 'restore' | 'mark' | 'upgrade' | 'verify';
  /**
   * The archive-verified recovery target.
   */
  recovery_point?: string;
  /**
   * The last replayed commit reported by PostgreSQL.
   */
  recovered_to?: string;
  started_at: string;
  finished_at?: string;
  /**
   * Total time from admission through cleanup.
   */
  duration_seconds?: number;
  /**
   * A stable reason for a failed rehearsal.
   */
  error_code?: string;
};

