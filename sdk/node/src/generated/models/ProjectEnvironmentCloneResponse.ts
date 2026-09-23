/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Non-secret copy counts for an environment clone. Managed database or bucket data appears as shared only after explicit opt-in.
 */
export type ProjectEnvironmentCloneResponse = {
  configuration_copied: boolean;
  variables_copied: number;
  secrets_copied: number;
  workloads_copied: number;
  bindings_copied: number;
  shared_resources: Array<'domains' | 'policies' | 'routes' | 'managed_postgres_data' | 'object_storage_bucket_data'>;
};

