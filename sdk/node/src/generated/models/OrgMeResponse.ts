/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OrgWithRole } from './OrgWithRole.js';
/**
 * GET /v1/orgs/me response. Without an X-Active-Org / ?org= hint,
 * `org` is the caller's personal organization. It is null only for a
 * legacy account that predates the personal-org backfill.
 *
 */
export type OrgMeResponse = {
  /**
   * The active org + caller's role, or null only for a legacy account without a personal organization.
   */
  org: OrgWithRole;
};

