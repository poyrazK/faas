/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable major upgrade progress. Provider identity and credentials are never returned; existing connections are interrupted once.
 */
export type ManagedPostgresMajorUpgrade = {
  id: string;
  database_id: string;
  from_postgres_major: number;
  target_postgres_major: number;
  generation: number;
  state: 'pending' | 'succeeded' | 'failed';
  connection_interruption_expected: boolean;
  /**
   * While pending, the latest retry's diagnostic. Once failed, why: upgrade_refused (the cluster stays on its major unchanged) or upgrade_failed (restore to a point before the upgrade).
   */
  last_error_code?: string;
  created_at: string;
  completed_at?: string;
};

