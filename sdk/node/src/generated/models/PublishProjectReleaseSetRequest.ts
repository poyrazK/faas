/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A complete mapping of project workload slugs to live deployment UUIDs.
 */
export type PublishProjectReleaseSetRequest = {
  /**
   * Current release UUID, or empty for no graph. Presence selects checked activation; mandatory on the check route.
   */
  expected_active_release_id?: string;
  ttl_seconds: number;
  deployments: Record<string, string>;
};

