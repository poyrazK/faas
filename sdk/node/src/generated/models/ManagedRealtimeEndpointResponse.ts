/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable managed realtime endpoint configuration. Credentials are
 * write-only and represented by a constant mask on every response.
 *
 */
export type ManagedRealtimeEndpointResponse = {
  id: string;
  app_id: string;
  account_id: string;
  callback_url: string;
  connect_path: string;
  message_path: string;
  disconnect_path: string;
  callback_auth_token_masked: '***';
  auth_token_masked: '***';
  /**
   * When the previous static bearer credential stops being accepted during rotation.
   */
  auth_token_previous_expires_at?: string | null;
  auth_mode: 'none' | 'static_bearer' | 'oidc_jwt';
  auth_issuer?: string;
  auth_jwks_url?: string;
  auth_audience?: Array<string>;
  auth_algorithms?: Array<'RS256' | 'RS384' | 'RS512' | 'ES256' | 'ES384' | 'ES512'>;
  auth_required_claims?: Record<string, string>;
  allowed_origins: Array<string>;
  max_connections: number;
  max_message_bytes: number;
  max_connection_age_seconds: number;
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

