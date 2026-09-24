/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Existing tenant surface and its current certificate state.
 */
export type ApplyPlatformTenantSurfaceResponse = {
  id: string;
  app_id: string;
  name: string;
  status: 'pending' | 'active' | 'suspended';
  cert_state: 'none' | 'pending' | 'issued' | 'failed';
  action: 'link' | 'unchanged';
};

