/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Data delivered with mcp.contract.changed when a candidate that changed a listed MCP server's tool contract went live.
 */
export type MCPContractChangedWebhookPayload = {
  app_id: string;
  deployment_id: string;
  verdict: 'compatible' | 'review' | 'breaking' | 'unverified';
  baseline_sha256: string;
  /**
   * Null when the catalog could not be listed.
   */
  contract_sha256: string | null;
  overridden: boolean;
  tools: Array<string>;
  changes: number;
};

