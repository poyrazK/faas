/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessOAuthConnection } from './MCPReadinessOAuthConnection.js';
/**
 * Schedule, failure threshold and connection settings for continuous readiness probes.
 */
export type MCPReadinessMonitorConfig = {
  /**
   * Environment scope to pinned connection. Mutually exclusive with oauth_connection and secret_ref. Missing environments fail closed.
   */
  oauth_connections?: Record<string, MCPReadinessOAuthConnection>;
  oauth_connection?: MCPReadinessOAuthConnection;
  enabled: boolean;
  interval_seconds: number;
  failure_threshold: number;
  /**
   * Scoped app-secret reference; no plaintext credentials.
   */
  secret_ref?: string;
};

