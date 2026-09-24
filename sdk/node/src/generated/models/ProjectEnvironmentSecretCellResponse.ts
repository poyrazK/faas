/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One side of a secret comparison. Version is omitted when unknown; never contains secret material.
 */
export type ProjectEnvironmentSecretCellResponse = {
  present: boolean;
  value_hash?: string;
  version?: number;
  managed_by?: 'managed_postgres' | 'object_storage';
  binding_id?: string;
  credential_generation?: number;
};

