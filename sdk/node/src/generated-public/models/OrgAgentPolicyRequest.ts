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
  /**
   * Kinds of action every agent acting in the organization must have a person approve; omit to keep the current ones.
   */
  approval_categories?: Array<'delete' | 'production' | 'credentials' | 'billing'>;
  /**
   * Names of your own notification channels that get approval requests of agents acting in the organization; omit to keep them, send an empty list to remove them.
   */
  approval_channels?: Array<string>;
  /**
   * Ceiling, in cents a month, on the budget of every agent acting in the organization; omit to keep the current ceiling.
   */
  max_agent_budget_cents?: number;
  /**
   * Remove the ceiling on agents' budgets.
   */
  clear_max_agent_budget?: boolean;
};

