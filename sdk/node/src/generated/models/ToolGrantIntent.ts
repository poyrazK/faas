/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact immutable delegation to an existing agent key. Expiry uses microsecond precision; discovery, invocation consent and single-dispatch authority are checked separately.
 */
export type ToolGrantIntent = {
  /**
   * Exact existing native agent connection key identity
   */
  key_id: string;
  version: string;
  name: string;
  /**
   * Future expiry within 30 days and no later than the connection key expiry
   */
  expires_at: string;
  /**
   * Owner permits calls through this exact grant; hosted connections also require independent invocation consent and operator enablement
   */
  allow_invocation: boolean;
  /**
   * Optional existing secret:MCP_* access token in the exact app default environment; exclusive with credential_connection_id. Never a value.
   */
  credential_secret_ref?: string;
  /**
   * Optional exact owner-approved managed execution connection; private invocation grants only and exclusive with credential_secret_ref.
   */
  credential_connection_id?: string;
};

