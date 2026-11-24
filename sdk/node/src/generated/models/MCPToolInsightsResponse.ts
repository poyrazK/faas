/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPToolInsight } from './MCPToolInsight.js';
/**
 * Per-tool MCP usage and estimated compute for one app over the trailing window.
 */
export type MCPToolInsightsResponse = {
  app: string;
  /**
   * Trailing window every figure covers.
   */
  window: string;
  observed_at: string;
  telemetry: 'reporting' | 'not_observed';
  last_event?: string;
  /**
   * Plan RAM; compute is estimated at memory_mb + 8.
   */
  memory_mb: number;
  tools: Array<MCPToolInsight>;
};

