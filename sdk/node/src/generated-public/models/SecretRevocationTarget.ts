/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Non-sensitive snapshot of one authorized workload's deletion acknowledgement state.
 */
export type SecretRevocationTarget = {
  instance_id: string;
  workload_name?: string;
  runtime_state: string;
  reload_support: 'enabled' | 'disabled' | 'unknown';
  status: 'pending' | 'applied' | 'failed';
  ack_revision?: string;
  ack_at?: string;
  error_code?: 'application_reload_failed';
};

