/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPListingTool } from './MCPListingTool.js';
/**
 * A sold MCP server's listing to publish: the tools are those the server lists now.
 */
export type PutMCPListingRequest = {
  title: string;
  /**
   * One or two sentences on what the server does.
   */
  summary?: string;
  /**
   * The MCP endpoint, https on the app's own host.
   */
  endpoint: string;
  tools: Array<MCPListingTool>;
  /**
   * MCP registry name, such as io.github.<user>/<server>; enables server.json.
   */
  registry_name?: string;
  /**
   * Show the open customer count on the page.
   */
  show_usage?: boolean;
  /**
   * The live server's format-2 MCP catalog contract, at most 1,000,000 bytes. Production deploys are then held to it. Without it a republish keeps the contract production deploys are held to now, and its probe_secret, unless drop_contract is set.
   */
  contract?: Record<string, any>;
  /**
   * Republish without a contract, which ends the tool contract gate. Exclusive with contract.
   */
  drop_contract?: boolean;
  /**
   * App secret holding a bearer token the gate sends when listing a candidate's tools, for a server that checks credentials itself. Needs a contract, sent or kept; empty on a republish that keeps the contract keeps the current one.
   */
  probe_secret?: string;
};

