/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Actionable connection health metadata without sealed grants or provider responses.
 */
export type MCPConnectionHealthIssue = {
  /**
   * Environment containing the MCP connection with this health issue. Empty means default.
   */
  scope?: string;
  id: string;
  app_id: string;
  app_slug: string;
  role: string;
  kind: 'reauthorization_required' | 'renewal_failed' | 'retirement_expiring' | 'issuance_unknown';
  state: string;
  app_deleted?: boolean;
  retirement_id?: string;
  expires_at?: string;
  recovery_action: string;
  recovery_path: string;
};

