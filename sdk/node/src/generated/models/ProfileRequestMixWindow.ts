/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileQuery } from './ProfileQuery.js';
import type { ProfileRequestMixGroup } from './ProfileRequestMixGroup.js';
/**
 * Frozen request-count summary for one profile query window.
 */
export type ProfileRequestMixWindow = {
  query: ProfileQuery;
  total: number;
  routes: Array<ProfileRequestMixGroup>;
  statuses: Array<ProfileRequestMixGroup>;
  truncated: boolean;
};

