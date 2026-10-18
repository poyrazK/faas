/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Public OAuth connection health; sealed credentials and source fingerprint are omitted from responses.
 */
export type MCPCatalogObserverOAuthStatus = {
  /**
   * Internal issuance reservation identity omitted from public responses
   */
  readonly retirement_id?: string;
  state: 'connected' | 'reauthorization_required';
  /**
   * Reconnect is required or credentials failed within fifteen minutes of expiry
   */
  readonly reauthorization_needed?: boolean;
  expires_at?: string;
  last_renewal_at?: string;
  /**
   * Internal sealed storage omitted from public responses
   */
  readonly ciphertext?: string;
  /**
   * Internal source fingerprint omitted from public responses
   */
  readonly source_sha256?: string;
  granted_scopes?: Array<string>;
};

