/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One side of a secret comparison; never contains secret material.
 */
export type ProjectEnvironmentSecretCellResponse = {
  present: boolean;
  value_hash?: string;
  managed_by?: 'managed_postgres' | 'object_storage';
  binding_id?: string;
  credential_generation?: number;
};

