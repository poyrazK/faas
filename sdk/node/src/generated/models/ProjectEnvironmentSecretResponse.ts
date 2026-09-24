/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe secret metadata. Version is omitted for legacy secrets with unknown history; secret values and ciphertext are never included.
 */
export type ProjectEnvironmentSecretResponse = {
  key: string;
  value_hash?: string;
  version?: number;
  managed_by?: 'managed_postgres' | 'object_storage';
  binding_id?: string;
  credential_generation?: number;
  updated_at?: string;
};

