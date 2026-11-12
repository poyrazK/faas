/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Moves a branch's expiry.
 */
export type ExtendProjectEnvironmentBranchRequest = {
  /**
   * The time from now the branch lives, such as 48h. Empty takes the default (168h).
   */
  ttl?: string;
};

