/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * RFC 8693 token exchange response with an opaque deploy bearer.
 */
export type OAuthTokenExchangeResponse = {
  /**
   * Opaque `fp_oidc_…` bearer for deploy routes.
   */
  access_token: string;
  issued_token_type: 'urn:ietf:params:oauth:token-type:access_token';
  token_type: 'Bearer';
  /**
   * Seconds until the bearer expires.
   */
  expires_in: number;
  scope: 'deploy:write';
};

