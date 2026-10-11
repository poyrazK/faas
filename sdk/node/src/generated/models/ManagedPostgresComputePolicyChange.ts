/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable compute policy change progress. Provider identity and credentials are never returned; existing connections may be interrupted.
 */
export type ManagedPostgresComputePolicyChange = {
  id: string;
  database_id: string;
  from_scale_to_zero: boolean;
  target_scale_to_zero: boolean;
  generation: number;
  state: 'pending' | 'succeeded';
  connection_interruption_expected: boolean;
  /**
   * Safe policy-change diagnostic; an uncertain outcome keeps the accepted intent pending for recovery.
   */
  last_error_code?: string;
  created_at: string;
  completed_at?: string;
};

