/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPCatalogObserverOAuthConfig } from './MCPCatalogObserverOAuthConfig.js';
/**
 * Durable RFC 7009 progress; acknowledgement also covers tokens already invalid at the provider. Private fields are omitted from all public projections.
 */
export type MCPCatalogOAuthRevocation = {
  id: string;
  state: 'pending' | 'provider_acknowledged' | 'manual_required';
  attempts: number;
  next_attempt_at: string;
  failure_reason?: 'credential_unavailable' | 'provider_unavailable';
  acknowledged_at?: string;
  refresh_acknowledged: boolean;
  access_acknowledged: boolean;
  /**
   * Internal frozen binding omitted from responses
   */
  readonly private_config?: MCPCatalogObserverOAuthConfig;
  /**
   * Internal sealed client credential captured at disconnect and omitted from responses
   */
  readonly private_client_ciphertext?: string;
  /**
   * Internal sealed retired grant omitted from responses
   */
  readonly private_ciphertext?: string;
  /**
   * Internal fence omitted from responses
   */
  readonly private_lease_id?: string;
  /**
   * Internal lease omitted from responses
   */
  readonly private_lease_until?: string;
};

