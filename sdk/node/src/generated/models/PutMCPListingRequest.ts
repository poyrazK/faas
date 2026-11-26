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
};

