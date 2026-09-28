/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Owner-controlled self-service customer creation policy; an absent policy is disabled.
 */
export type PlatformTenantConsumerProvisioningPolicyResponse = {
  tenant_id: string;
  enabled: boolean;
  max_consumers: number;
  updated_at?: string;
};

