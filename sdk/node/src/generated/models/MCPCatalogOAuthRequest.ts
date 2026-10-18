/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Account-authorized guided OAuth action bound to an exact role revision.
 */
export type MCPCatalogOAuthRequest = {
  action: 'begin' | 'complete' | 'disconnect' | 'check';
  role: string;
  revision: string;
  /**
   * Begin only; HTTP IPv4 loopback callback with an explicit port and callback path
   */
  redirect_uri?: string;
  /**
   * Complete only; one-time authorization state
   */
  state?: string;
  /**
   * Complete only; authorization code exchanged with sealed PKCE verifier
   */
  code?: string;
  /**
   * Consume a denied authorization callback without a token exchange
   */
  denied?: boolean;
};

