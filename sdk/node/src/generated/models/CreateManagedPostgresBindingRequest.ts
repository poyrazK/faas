/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Request to inject a managed database credential into an app environment. Access values are provider-neutral; the selected database backend may support only a subset.
 */
export type CreateManagedPostgresBindingRequest = {
  app_id: string;
  scope: string;
  environment_key: string;
  /**
   * Portable credential mode. Unsupported modes are rejected before a binding is reserved.
   */
  access: 'read_write' | 'read_only';
};

