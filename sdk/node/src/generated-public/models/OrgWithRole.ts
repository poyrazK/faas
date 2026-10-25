/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OrgResponse } from './OrgResponse.js';
/**
 * OrgResponse + the caller's role on the active org. Used by
 * GET /v1/orgs/me (`OrgMeResponse.org`). The role field is
 * a closed enum: owner|admin|developer|viewer|billing.
 *
 */
export type OrgWithRole = (OrgResponse & {
  role: 'owner' | 'admin' | 'developer' | 'viewer' | 'billing';
});

