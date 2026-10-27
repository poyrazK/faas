/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for `POST /v1/auth/oidc/exchange` (ADR-101).
 */
export type OIDCExchangeRequest = {
  /**
   * IdP identifier (`github`, `gitlab`, `circleci`, or generic `oidc`). Used for audit attribution only — the issuer is pinned in the JWT `iss` claim.
   */
  provider: string;
  /**
   * Raw IdP-issued JWT (the IdP token from `ACTIONS_ID_TOKEN_REQUEST_TOKEN` etc.).
   */
  token: string;
  /**
   * The `aud` claim the customer pinned in the action. Must match the trust policy's `audience` array verbatim.
   */
  aud: string;
  /**
   * Optional app slug for audit attribution. Empty skips the audit app attribution.
   */
  app?: string;
  /**
   * Optional closed OIDC capability profile. Omit to retain the historical deploy:write bearer; environment-preflight grants only project-environment read and qualification scopes.
   */
  capability?: 'environment-preflight';
};

