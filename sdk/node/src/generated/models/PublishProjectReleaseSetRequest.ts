/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A complete mapping of project workload slugs to live deployment UUIDs.
 */
export type PublishProjectReleaseSetRequest = {
  ttl_seconds: number;
  deployments: Record<string, string>;
};

