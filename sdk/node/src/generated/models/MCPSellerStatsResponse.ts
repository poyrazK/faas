/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPSellerCustomerTotals } from './MCPSellerCustomerTotals.js';
import type { MCPSellerNearLimit } from './MCPSellerNearLimit.js';
import type { MCPSellerStatsMonth } from './MCPSellerStatsMonth.js';
import type { MCPSellerStatsTool } from './MCPSellerStatsTool.js';
/**
 * A sold MCP server's customers, tools and revenue over UTC calendar months.
 */
export type MCPSellerStatsResponse = {
  app: string;
  generated_at: string;
  /**
   * Oldest first; the last is the current month to date.
   */
  months: Array<MCPSellerStatsMonth>;
  tools: Array<MCPSellerStatsTool>;
  near_limit: Array<MCPSellerNearLimit>;
  customers: MCPSellerCustomerTotals;
  /**
   * False when the current month's estimate left customers out.
   */
  estimate_complete: boolean;
};

