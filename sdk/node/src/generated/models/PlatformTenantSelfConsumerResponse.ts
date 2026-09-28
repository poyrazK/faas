/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Minimal linked-consumer identity used to target tenant-managed credentials; app IDs and account metadata are omitted.
 */
export type PlatformTenantSelfConsumerResponse = {
  consumer_id: string;
  external_ref: string;
  name: string;
  status: 'active' | 'revoked';
};

