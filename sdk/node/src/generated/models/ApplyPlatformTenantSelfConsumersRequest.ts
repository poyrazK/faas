/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * All-or-nothing customer onboarding bundle across surfaces already linked to the token's tenant; no app or tenant identifier is accepted.
 */
export type ApplyPlatformTenantSelfConsumersRequest = {
  external_ref: string;
  name: string;
  surface_ids: Array<string>;
  dry_run?: boolean;
};

