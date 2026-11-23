/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit OAuth refresh grant for one app and role; credentials are app secret references in the connection environment.
 */
export type MCPCatalogObserverOAuthConfig = {
  /**
   * Explicit HTTPS RFC 7009 revocation endpoint; omission requires manual provider revocation
   */
  revocation_endpoint?: string;
  /**
   * Defaults to token authentication except Google none
   */
  revocation_auth_method?: 'none' | 'client_secret_basic' | 'client_secret_post';
  /**
   * Explicit public HTTPS token endpoint without query or fragment; redirects and private egress are refused
   */
  token_endpoint: string;
  client_id: string;
  /**
   * App secret containing the initial refresh token; rotation resets the connection
   */
  refresh_token_secret_ref?: string;
  /**
   * Optional app secret for OAuth client_secret_basic authentication
   */
  client_secret_ref?: string;
  /**
   * Use browser authorization; mutually exclusive with an initial refresh secret
   */
  guided?: boolean;
  /**
   * Explicit HTTPS browser authorization endpoint required for guided connections
   */
  authorization_endpoint?: string;
  scopes?: Array<string>;
  /**
   * Explicit provider behavior; Google requests offline consent and OIDC adds openid and offline_access
   */
  profile?: 'generic' | 'google' | 'oidc';
  /**
   * Explicit token authentication; defaults to public none or confidential basic except Google confidential post
   */
  token_auth_method?: 'none' | 'client_secret_basic' | 'client_secret_post';
  /**
   * Registered HTTPS console callback for guided browser authorization; omitted for loopback flows
   */
  browser_redirect_uri?: string;
  /**
   * Guided loopback callback port; 0 chooses a dynamic port
   */
  callback_port?: number;
  /**
   * Reviewed HTTPS MCP resource indicator sent unchanged in authorization
   */
  resource?: string;
  /**
   * Reviewed provider issuer paired with discovery_url
   */
  issuer?: string;
  /**
   * Explicit guarded metadata URL; endpoint mismatches and declared incompatibilities block new authorization
   */
  discovery_url?: string;
};

