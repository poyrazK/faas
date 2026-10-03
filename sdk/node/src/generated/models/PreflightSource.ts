/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A validated public GitHub repository reference. Every field has passed
 * the character and length rules, so it is safe to interpolate upstream.
 *
 */
export type PreflightSource = {
  owner: string;
  repo: string;
  /**
   * Branch or tag parsed from the input; empty means the default branch.
   */
  ref?: string;
};

