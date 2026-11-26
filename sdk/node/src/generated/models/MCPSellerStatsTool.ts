/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPSellerStatsAmount } from './MCPSellerStatsAmount.js';
/**
 * One MCP tool's units and customers over the window.
 */
export type MCPSellerStatsTool = {
  tool: string;
  units: number;
  customers: number;
  /**
   * This month's estimated revenue for the tool.
   */
  revenue?: Array<MCPSellerStatsAmount>;
};

