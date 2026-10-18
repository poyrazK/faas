/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPCatalogObserverOAuthConfig } from './MCPCatalogObserverOAuthConfig.js';
/**
 * Opt-in platform observer intent for one MCP role using its canonical app origin and an optional sealed secret reference.
 */
export type MCPCatalogObserverConfig = {
  enabled: boolean;
  paused: boolean;
  interval_seconds: number;
  timeout_seconds: number;
  endpoint: string;
  /**
   * App secret in the connection environment named secret:NAME; its current value is loaded for each check
   */
  secret_ref?: string;
  oauth?: MCPCatalogObserverOAuthConfig;
};

