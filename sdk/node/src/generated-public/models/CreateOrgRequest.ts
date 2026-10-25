/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * POST /v1/orgs body. Slug matches `OrgSlugPattern`
 * (lowercase alphanumeric + dashes, 3..32 chars); name is
 * trimmed-non-empty, capped at 256 chars. Personal orgs
 * cannot be created via this endpoint — every account
 * already owns an immutable personal org (PR 3 backfill).
 *
 */
export type CreateOrgRequest = {
  slug: string;
  name: string;
};

