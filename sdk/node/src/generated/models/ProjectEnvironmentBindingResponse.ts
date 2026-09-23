/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ProjectEnvironmentBindingResponse = {
  kind: 'managed_postgres' | 'object_storage';
  binding_id: string;
  credential_generation?: number;
  secret_keys: Array<string>;
};

