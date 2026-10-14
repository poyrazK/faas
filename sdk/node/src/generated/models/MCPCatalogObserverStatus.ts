/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPCatalogOAuthFlow } from './MCPCatalogOAuthFlow.js';
import type { MCPCatalogOAuthRenewal } from './MCPCatalogOAuthRenewal.js';
import type { MCPCatalogObserverOAuthStatus } from './MCPCatalogObserverOAuthStatus.js';
/**
 * Managed observer scheduling and recovery metadata without credential values or raw source errors.
 */
export type MCPCatalogObserverStatus = {
  state: 'scheduled' | 'checking' | 'degraded' | 'overdue' | 'paused' | 'stopped';
  last_attempt_at?: string;
  last_success_at?: string;
  next_check_at?: string;
  failure_reason?: 'credential_unavailable' | 'source_unavailable' | 'comparison_unavailable';
  consecutive_failures: number;
  /**
   * Internal fencing metadata omitted from public monitor projections
   */
  readonly lease_id?: string;
  lease_until?: string;
  oauth?: MCPCatalogObserverOAuthStatus;
  oauth_renewal?: MCPCatalogOAuthRenewal;
  oauth_flow?: MCPCatalogOAuthFlow;
};

