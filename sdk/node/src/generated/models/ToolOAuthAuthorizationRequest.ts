/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Owner-only transient PKCE command; codes and callback state are never journaled as tool arguments.
 */
export type ToolOAuthAuthorizationRequest = {
  action: 'begin' | 'complete';
  /**
   * Begin only; exact http IPv4 loopback callback with a nonzero port and /callback path
   */
  redirect_uri?: string;
  /**
   * Complete only; exact nonce from the pending browser attempt
   */
  state?: string;
  /**
   * Complete only; transient provider authorization code
   */
  code?: string;
  /**
   * Complete a denied browser authorization without a code
   */
  denied?: boolean;
  /**
   * Optional callback issuer; must equal the immutable configured issuer
   */
  issuer?: string;
};

