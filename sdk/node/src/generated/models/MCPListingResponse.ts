/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPListingTool } from './MCPListingTool.js';
/**
 * A sold MCP server's published listing, as its owner sees it.
 */
export type MCPListingResponse = {
  app: string;
  title: string;
  /**
   * What the server does, as the seller wrote it.
   */
  summary: string;
  endpoint: string;
  tools: Array<MCPListingTool>;
  registry_name?: string;
  show_usage: boolean;
  /**
   * Bumped on every publish.
   */
  version: number;
  published_at: string;
  /**
   * The public listing page.
   */
  page_url: string;
  /**
   * The registry server.json, when registry_name is set.
   */
  server_json_url?: string;
};

