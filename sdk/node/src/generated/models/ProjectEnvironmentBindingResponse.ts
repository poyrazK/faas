/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Managed resource binding and its target-scoped credential metadata.
 */
export type ProjectEnvironmentBindingResponse = {
  kind: 'managed_postgres' | 'object_storage';
  binding_id: string;
  credential_generation?: number;
  secret_keys: Array<string>;
};

