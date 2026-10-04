/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { GlobalAppOwnerResponse } from './GlobalAppOwnerResponse.js';
import type { GlobalAppTargetResponse } from './GlobalAppTargetResponse.js';
/**
 * Account or organization owned global identity. Shared organizations can register identities; regional attachment requires the organization grant protocol. Select a workspace using X-Active-Org or org, or an organization-bound API key. No runtime health or deployment claim.
 */
export type GlobalAppResponse = {
  owner: GlobalAppOwnerResponse;
  id: string;
  slug: string;
  routing_policy: 'all_selected_regions';
  created_at: string;
  targets: Array<GlobalAppTargetResponse>;
};

