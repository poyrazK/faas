/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPContractCheck } from './MCPContractCheck.js';
import type { MCPContractOverride } from './MCPContractOverride.js';
/**
 * The tool contract a listed MCP server's production deploys are held to.
 */
export type MCPContractResponse = {
  app: string;
  baseline_sha256: string;
  /**
   * The accepted candidate the baseline comes from; absent when it is the listing's.
   */
  baseline_deployment_id?: string;
  listing_version: number;
  tools: Array<string>;
  probe_secret?: string;
  checks: Array<MCPContractCheck>;
  overrides: Array<MCPContractOverride>;
};

