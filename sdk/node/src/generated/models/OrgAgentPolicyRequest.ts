/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Whether agents may act in the organization, and the capabilities they may get.
 */
export type OrgAgentPolicyRequest = {
  /**
   * Whether hosted MCP connections and agent keys may act in the organization.
   */
  agents_allowed: boolean;
  /**
   * Capabilities agents may get beyond inspection; empty for read-only.
   */
  allowed_scopes: Array<'deploy:write' | 'secrets:request' | 'storage:manage' | 'postgres:manage'>;
};

