/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable replay evidence for one operator-declared source write pause.
 */
export type ManagedPostgresMigrationVerification = {
  verification_id: string;
  state: 'verifying' | 'verified' | 'paused' | 'blocked';
  reason?: string;
  /**
   * Flushed subscriber origin position sampled after seeing the marker.
   */
  applied_lsn?: string;
  sequences_synced: number;
  verified_at?: string;
};

