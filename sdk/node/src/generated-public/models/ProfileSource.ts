/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Deployment-recorded GitHub provenance. Links use a full immutable commit SHA; uploaded or generated source is not verified against that commit. Unavailable provenance is explicit.
 */
export type ProfileSource = {
  available: boolean;
  repository?: string;
  commit_sha?: string;
  commit_url?: string;
  reason?: string;
};

