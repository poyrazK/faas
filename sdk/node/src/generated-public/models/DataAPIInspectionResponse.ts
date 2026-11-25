/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type DataAPIInspectionResponse = {
  task_id: string;
  app_id: string;
  deployment_id: string;
  database_id: string;
  binding_id: string;
  credential_generation: number;
  task_status: 'queued' | 'restoring' | 'running' | 'succeeded' | 'failed' | 'timed_out' | 'cancelled';
  binding_identity: {
    status: 'verified' | 'mismatch' | 'unknown';
    reason: string;
  };
  /**
   * Bounded catalog metadata, present only when execution and TLS trust were verified.
   */
  observation?: Record<string, any>;
};

