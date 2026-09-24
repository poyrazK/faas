/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * App-local surface to create or reuse by account-level name, with an additive set of hostnames.
 */
export type ApplyPlatformTenantSurfaceRequest = {
  app_id: string;
  name: string;
  cert_kind?: 'per_host_san';
  hostnames: Array<string>;
};

