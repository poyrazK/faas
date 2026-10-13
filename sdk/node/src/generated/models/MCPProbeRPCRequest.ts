/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Single MCP JSON-RPC initialize, initialized notification, discovery, or catalog-list request. Mutating and data-reading methods are rejected.
 */
export type MCPProbeRPCRequest = {
  jsonrpc: string;
  id?: (string | number);
  method: 'initialize' | 'notifications/initialized' | 'server/discover' | 'tools/list' | 'resources/list' | 'resources/templates/list' | 'prompts/list';
  params?: Record<string, any>;
};

