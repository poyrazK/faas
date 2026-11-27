/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for `POST /v1/anonymous/sessions`.
 */
export type AnonymousSessionRequest = {
  /**
   * The tool creating the session, such as gregale-cli.
   */
  client?: string;
  /**
   * Must be true: deploying without an account accepts the Terms of Service.
   */
  accept_terms: boolean;
};

