/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One fixed-name bounded qualification outcome containing no native database facts or secret material.
 */
export type HostedTaskBindingResult = {
  name: 'namespace' | 'owner_keys' | 'configured_payload_keys' | 'database_schema' | 'runtime_roles' | 'migration_owner' | 'observer_read_only';
  status: 'not_checked' | 'passed' | 'failed';
};

