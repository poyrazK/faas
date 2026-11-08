/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable compute resize progress. Provider identity and credentials are never returned; existing connections may be interrupted.
 */
export type ManagedPostgresResize = {
  id: string;
  database_id: string;
  from_class: 'development' | 'burstable' | 'production';
  target_class: 'development' | 'burstable' | 'production';
  generation: number;
  state: 'pending' | 'succeeded' | 'failed';
  connection_interruption_expected: boolean;
  /**
   * Safe diagnostic code. A failed resize retains the confirmed source configuration; uncertain outcomes remain pending.
   */
  last_error_code?: string;
  created_at: string;
  completed_at?: string;
};

