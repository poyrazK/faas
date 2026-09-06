/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Workload binding metadata. Credentials are delivered through the app secret surface and never returned here.
 */
export type ManagedPostgresBinding = {
  id: string;
  database_id: string;
  app_id: string;
  scope: string;
  environment_key: string;
  access: 'read_write' | 'read_only';
  credential_generation: number;
  state: 'provisioning' | 'ready' | 'deleting' | 'failed' | 'deleted';
  last_error_code?: string | null;
  created_at: string;
  updated_at: string;
};

