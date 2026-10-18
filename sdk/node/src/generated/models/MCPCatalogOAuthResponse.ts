/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPCatalogOAuthCheck } from './MCPCatalogOAuthCheck.js';
/**
 * Guided OAuth outcome; token and verifier values are never returned.
 */
export type MCPCatalogOAuthResponse = {
  revision: string;
  state: 'authorization_pending' | 'connected' | 'disconnected' | 'compatible' | 'unsupported' | 'unverified' | 'unavailable';
  authorization_url?: string;
  /**
   * One-time state to validate the local browser callback
   */
  callback_state?: string;
  provider_revocation?: 'not_required' | 'pending' | 'provider_acknowledged' | 'manual_required';
  checks?: Array<MCPCatalogOAuthCheck>;
};

