/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe secret metadata. Secret values and ciphertext are never included.
 */
export type ProjectEnvironmentSecretResponse = {
  key: string;
  value_hash?: string;
  managed_by?: 'managed_postgres' | 'object_storage';
  binding_id?: string;
  credential_generation?: number;
  updated_at?: string;
};

