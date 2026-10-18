/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPCatalogObserverConfig } from './MCPCatalogObserverConfig.js';
/**
 * Reviewed baseline and observer settings for one independently monitored MCP role.
 */
export type MCPCatalogMonitorConfigRequest = {
  /**
   * Environment selected when configuring catalog monitoring. Omit for default.
   */
  scope?: string;
  observer?: MCPCatalogObserverConfig;
  /**
   * Environment variable name only; credentials are never stored
   */
  token_env?: string;
  role?: string;
  enabled: boolean;
  /**
   * Reviewed format-2 MCP catalog contract
   */
  baseline: Record<string, any>;
  freshness_seconds: number;
};

