/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Create an app-bound credential. Standard tokens receive deploy:write; agent=true permits a narrow MCP permission set while preserving the native app boundary.
 */
export type CreateDeployTokenRequest = {
  /**
   * Mint an app-bound headless MCP credential. Omit for a standard deploy token.
   */
  agent?: boolean;
  /**
   * Requires agent=true. Agent and apps:read are included; omitted scopes also include deploy:write.
   */
  scopes?: Array<'agent' | 'apps:read' | 'deploy:write' | 'env:write' | 'secrets:request' | 'storage:manage' | 'usage:read'>;
  label?: string;
  /**
   * Optional future expiry; defaults to 90 days and is capped at 365 days.
   */
  expires_at?: string;
};

