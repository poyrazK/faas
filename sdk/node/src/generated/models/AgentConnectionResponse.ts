/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One agent connected to the account. `id` is the API key the agent uses; revoking the connection revokes that key.
 */
export type AgentConnectionResponse = {
  /**
   * The agent's API key id.
   */
  id: string;
  /**
   * `hosted_mcp` for a browser sign-in through the hosted MCP server; `agent_key` for an API key with the `agent` scope.
   */
  kind: 'hosted_mcp' | 'agent_key';
  /**
   * The client name a hosted sign-in's consent page showed.
   */
  client_name?: string;
  /**
   * The host the client was verified at: the site serving its metadata document, or its HTTPS redirect host. Empty for a local client.
   */
  client_site?: string;
  /**
   * The key label.
   */
  label?: string;
  /**
   * The key's display prefix.
   */
  prefix: string;
  scopes: Array<string>;
  created_at: string;
  last_used_at?: string;
  expires_at?: string;
};

