/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Internal sealed one-time authorization state omitted from public monitor projections.
 */
export type MCPCatalogOAuthFlow = {
  /**
   * Internal browser session binding hash; the entire flow is omitted from public projections
   */
  readonly browser_session_sha256?: string;
  readonly state_sha256: string;
  readonly ciphertext: string;
  readonly expires_at: string;
  readonly consumed: boolean;
};

