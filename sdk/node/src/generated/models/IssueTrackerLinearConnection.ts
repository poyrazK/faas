/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Non-secret owner-scoped Linear connection metadata; disconnected records omit provider identity.
 */
export type IssueTrackerLinearConnection = {
  /**
   * Stored credential kind (api_key or oauth).
   */
  auth_method?: string;
  /**
   * OAuth health (ready, refreshing, reconnect_required or revoking); absent for API keys.
   */
  oauth_status?: string;
  /**
   * OAuth access token expiry; never includes credentials.
   */
  access_token_expires_at?: string;
  /**
   * True when a scoped signing secret is stored for Linear issue callbacks.
   */
  webhook_enabled?: boolean;
  /**
   * Provider callback path to append to the Gregale API origin when callbacks are enabled.
   */
  callback_path?: string;
  /**
   * Whether a verified API key or OAuth grant is stored.
   */
  connected: boolean;
  /**
   * Configuration revision used to fence concurrent changes.
   */
  revision: number;
  /**
   * Verified Linear organization identity.
   */
  workspace_id?: string;
  /**
   * Verified active Linear team identity.
   */
  team_id?: string;
  /**
   * Verified Linear user identity owning the credential.
   */
  viewer_id?: string;
};

