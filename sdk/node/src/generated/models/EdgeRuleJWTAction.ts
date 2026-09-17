/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Validates an inbound Bearer JWT against a JWKS endpoint. The
 * algorithm allowlist is asymmetric-only and the selected JWK's
 * `alg` metadata must match the JWT header; a JWK with `use=enc`
 * is rejected (RFC 8725 §3.1, RFC 7517).
 *
 */
export type EdgeRuleJWTAction = {
  issuer: string;
  audience?: Array<string>;
  jwks_url: string;
  algorithms: Array<'RS256' | 'RS384' | 'RS512' | 'ES256' | 'ES384' | 'ES512'>;
  required_claims?: Record<string, string>;
};

