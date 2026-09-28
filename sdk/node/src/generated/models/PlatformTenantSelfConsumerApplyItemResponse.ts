/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Result for one surface; a planned create omits consumer_id until the real apply runs.
 */
export type PlatformTenantSelfConsumerApplyItemResponse = {
  surface_id: string;
  consumer_id?: string;
  external_ref: string;
  name: string;
  status: 'active';
  action: 'create' | 'unchanged';
};

