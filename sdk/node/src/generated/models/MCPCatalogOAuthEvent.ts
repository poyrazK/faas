/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OAuth lifecycle metadata without token values or provider response bodies.
 */
export type MCPCatalogOAuthEvent = {
  id: string;
  at: string;
  kind: 'connected' | 'renewed' | 'reauthorization_required' | 'disconnected' | 'revocation_acknowledged' | 'revocation_manual_required' | 'credentials_discarded';
};

