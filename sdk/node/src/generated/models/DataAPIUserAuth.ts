/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The issuer, JWKS URL and audience the Data API verifies user tokens against. Live reads verify user tokens with the same settings.
 */
export type DataAPIUserAuth = {
  issuer: string;
  jwks_url: string;
  audience: string;
};

