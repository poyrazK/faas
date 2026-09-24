/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Planned or applied app consumer with its reconciliation action.
 */
export type ApplyPlatformTenantConsumerResponse = {
  /**
   * Absent when a dry run would create this consumer.
   */
  id?: string;
  app_id: string;
  external_ref: string;
  name: string;
  status: 'active';
  action: 'create' | 'link' | 'unchanged';
};

