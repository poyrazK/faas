/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Request to inject a managed database credential into an app environment.
 */
export type CreateManagedPostgresBindingRequest = {
  app_id: string;
  scope: string;
  environment_key: string;
  access: 'read_write' | 'read_only';
};

