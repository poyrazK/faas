/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Owner-only authorization metadata. Begin alone may return a consent URL and state; GET and completion omit them. No access token, refresh token, client secret or verifier is returned.
 */
export type ToolOAuthAuthorizationResponse = {
  connection_id: string;
  state: 'not_started' | 'authorization_pending' | 'authorization_uncertain' | 'connected' | 'reconnect_required' | 'authorization_denied';
  /**
   * Begin only; private owner browser URL bound to the retained attempt
   */
  authorization_url?: string;
  /**
   * Begin only; owner callback nonce
   */
  callback_state?: string;
  expires_at?: string;
};

