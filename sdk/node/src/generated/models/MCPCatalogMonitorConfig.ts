/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPCatalogObserverConfig } from './MCPCatalogObserverConfig.js';
/**
 * Server-issued revision and retained settings for a configured MCP catalog role.
 */
export type MCPCatalogMonitorConfig = {
  observer?: MCPCatalogObserverConfig;
  /**
   * Token environment reference selected by observers; no secret value is retained
   */
  token_env?: string;
  role?: string;
  enabled: boolean;
  revision: string;
  baseline: Record<string, any>;
  baseline_sha256: string;
  freshness_seconds: number;
};

