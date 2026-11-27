/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A recorded transition of a primary storage incident.
 */
export type ManagedPostgresStorageAlert = {
  incident_id: string;
  state: 'low_space' | 'read_only' | 'recovered';
  sampled_at: string;
  total_bytes: number;
  free_bytes: number;
  /**
   * Automatic storage growth status when the transition was recorded.
   */
  growth_reason?: string;
};

