/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A linked tenant surface summary; managed_by_platform_tenant is true only when a platform-tenant apply created it. Use the app endpoint for hostnames.
 */
export type PlatformTenantSurfaceResponse = {
  id: string;
  app_id: string;
  name: string;
  status: string;
  /**
   * True when this surface originated in the account owner's tenant bundle apply; absent otherwise.
   */
  managed_by_platform_tenant?: boolean;
};

