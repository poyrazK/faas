/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPCatalogOAuthRevocation } from './MCPCatalogOAuthRevocation.js';
import type { MCPOAuthRetirementEvent } from './MCPOAuthRetirementEvent.js';
/**
 * Account-owned grant retirement metadata surviving app deletion; sealed data is omitted from public responses.
 */
export type MCPOAuthRetirement = {
  /**
   * Environment of the retired MCP OAuth connection. Empty means default.
   */
  scope?: string;
  id: string;
  app_id: string;
  app_slug: string;
  role: string;
  /**
   * Retirement cause or unknown issuance outcome
   */
  reason: string;
  state: 'awaiting_issuance' | 'pending' | 'held' | 'installed' | 'manual_required' | 'provider_acknowledged' | 'manually_acknowledged' | 'expired';
  created_at: string;
  /**
   * Seven-day maximum sealed credential retention deadline
   */
  retain_until: string;
  revocation: MCPCatalogOAuthRevocation;
  /**
   * Explicit operator authorization to retire a grant despite a matching active connection
   */
  allow_active_grant_revocation: boolean;
  history: Array<MCPOAuthRetirementEvent>;
};

