/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { GlobalAppTargetResponse } from './GlobalAppTargetResponse.js';
/**
 * Account-owned global identity and selected regional identities. No runtime health or deployment claim.
 */
export type GlobalAppResponse = {
  id: string;
  slug: string;
  routing_policy: 'all_selected_regions';
  created_at: string;
  targets: Array<GlobalAppTargetResponse>;
};

