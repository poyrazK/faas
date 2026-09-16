/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe receipt for the isolated developer database and its app binding. Credentials are injected into DATABASE_URL and never returned.
 */
export type DevPostgresResponse = {
  database_id: string;
  name: string;
  state: 'provisioning' | 'ready' | 'updating' | 'deleting' | 'failed' | 'deleted';
  binding_id: string;
  binding_state: 'provisioning' | 'ready' | 'deleting' | 'failed' | 'deleted';
  environment_key: string;
};

