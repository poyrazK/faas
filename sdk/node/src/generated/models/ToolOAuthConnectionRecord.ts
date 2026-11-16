/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ToolOAuthConnectionIntent } from './ToolOAuthConnectionIntent.js';
/**
 * Owner-only immutable configuration plus credential-free renewal metadata. Binding excludes routine token generations; source replacement requires a new connection and grant.
 */
export type ToolOAuthConnectionRecord = {
  id: string;
  account_id: string;
  app_id: string;
  project_id?: string;
  binding_sha256: string;
  /**
   * Initial source ciphertext digest in manual mode or versioned source-mode marker in guided mode
   */
  source_sha256: string;
  client_source_sha256?: string;
  intent: ToolOAuthConnectionIntent;
  created_at: string;
  revoked_at?: string;
  state: 'authorization_pending' | 'refresh_required' | 'ready' | 'refreshing' | 'reconnect_required' | 'revoked';
  expires_at?: string;
  generation: number;
  recovery_action?: 'connect' | 'reconnect' | 'review_connection';
};

