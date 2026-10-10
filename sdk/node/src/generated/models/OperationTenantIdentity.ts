/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Tenant credential identity used to bind a private local submission receipt; optional submission fence returns 409 operation_identity_conflict if the principal changes.
 */
export type OperationTenantIdentity = {
  account_id: string;
  platform_tenant_id: string;
};

