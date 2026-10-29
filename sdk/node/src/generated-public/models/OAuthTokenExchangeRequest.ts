/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * RFC 8693 form-encoded request profile for the OIDC exchange endpoint.
 * Gregale requires one `audience` because it selects the account trust
 * policy, accepts JWT subject tokens only, and issues only deploy:write
 * access tokens. `resource` and actor-token delegation are unsupported.
 *
 */
export type OAuthTokenExchangeRequest = {
  grant_type: 'urn:ietf:params:oauth:grant-type:token-exchange';
  /**
   * IdP-issued JWT to exchange for a short-lived deploy bearer.
   */
  subject_token: string;
  subject_token_type: 'urn:ietf:params:oauth:token-type:jwt';
  /**
   * Trust-policy audience pinned in the subject JWT.
   */
  audience: string;
  requested_token_type?: 'urn:ietf:params:oauth:token-type:access_token';
  scope?: 'deploy:write';
};

