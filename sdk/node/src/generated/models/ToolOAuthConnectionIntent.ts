/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Owner-approved immutable execution authority. Guided public clients use browser PKCE and no source-secret references; manually provisioned connections use existing app-secret references. HTTPS provider URLs cannot include userinfo, queries or fragments. No discovery is performed.
 */
export type ToolOAuthConnectionIntent = {
  /**
   * Enable owner-only authorization-code S256 PKCE for a public client
   */
  guided?: boolean;
  /**
   * Explicit reviewed HTTPS endpoint for guided mode only
   */
  authorization_endpoint?: string;
  app: string;
  scope: 'default';
  issuer: string;
  token_endpoint: string;
  client_id: string;
  resource: string;
  scopes: Array<string>;
  /**
   * Manual mode only; existing ordinary secret:MCP_* app secret containing the initial refresh token
   */
  refresh_token_secret_ref?: string;
  /**
   * Manual mode only; existing ordinary secret:MCP_* app secret required for client_secret_basic
   */
  client_secret_ref?: string;
  token_auth_method: 'none' | 'client_secret_basic';
};

