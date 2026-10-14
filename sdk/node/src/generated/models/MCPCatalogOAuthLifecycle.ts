/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPCatalogOAuthEvent } from './MCPCatalogOAuthEvent.js';
import type { MCPCatalogOAuthRevocation } from './MCPCatalogOAuthRevocation.js';
/**
 * Durable local connection state, provider revocation progress and bounded metadata audit history.
 */
export type MCPCatalogOAuthLifecycle = {
  credential_state?: 'credential_changed' | 'retirement_held' | 'reconnect_required';
  local_state: 'connected' | 'disconnected' | 'reauthorization_required';
  revocation?: MCPCatalogOAuthRevocation;
  history: Array<MCPCatalogOAuthEvent>;
};

