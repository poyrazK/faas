/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Opt-in MCP resource-server policy on a JWT edge rule. Requires a
 * canonical HTTPS resource, matching JWT audience, match_path=**,
 * and no method or header selectors. The gateway serves OAuth resource
 * metadata, rejects expired credentials, and enforces JSON-RPC execution
 * scopes. Catalog filtering and task ownership remain application duties.
 *
 */
export type MCPResourcePolicy = {
  resource: string;
  scopes?: Array<string>;
  allowed_origins?: Array<string>;
  tool_scopes?: Record<string, Array<string>> | null;
  /**
   * Absolute URIs or simple variable templates; every matching entry must authorize access.
   */
  resource_scopes?: Record<string, Array<string>> | null;
  prompt_scopes?: Record<string, Array<string>> | null;
};

