/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPCatalogObserverOAuthStatus } from './MCPCatalogObserverOAuthStatus.js';
/**
 * Renewal recovery metadata. Private operation fields and sealed results are omitted from public responses.
 */
export type MCPCatalogOAuthRenewal = {
  outcome?: 'response_recorded' | 'issuance_unknown';
  state: 'pending' | 'saved' | 'installed' | 'failed' | 'reauthorization_required';
  started_at: string;
  readonly private_id?: string;
  readonly private_source_sha256?: string;
  readonly private_lease_id?: string;
  private_result?: MCPCatalogObserverOAuthStatus;
};

