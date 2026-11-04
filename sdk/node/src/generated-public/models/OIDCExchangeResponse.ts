/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for `POST /v1/auth/oidc/exchange` success response.
 */
export type OIDCExchangeResponse = {
  /**
   * Opaque bearer, format `fp_oidc_<48 hex>`. Use in `Authorization: Bearer …` on routes allowed by the returned scope profile.
   */
  bearer: string;
  /**
   * Seconds until the bearer expires (300 today).
   */
  expires_in: number;
  /**
   * Opaque row id (UUID). Useful for log correlation / audit reads.
   */
  token_id: string;
  /**
   * The fixed scopes granted to this bearer.
   */
  scopes?: Array<string>;
};

