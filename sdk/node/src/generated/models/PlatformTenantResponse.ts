/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Account-level end customer; links and credentials are separate resources.
 */
export type PlatformTenantResponse = {
  id: string;
  external_ref: string;
  name: string;
  status: 'active' | 'suspended';
  created_at: string;
  updated_at: string;
};

