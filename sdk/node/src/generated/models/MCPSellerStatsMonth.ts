/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPSellerStatsAmount } from './MCPSellerStatsAmount.js';
/**
 * One UTC calendar month of a sold MCP server's customers, usage and revenue.
 */
export type MCPSellerStatsMonth = {
  /**
   * YYYY-MM (UTC).
   */
  month: string;
  current: boolean;
  signed_up: number;
  /**
   * Sign-ups by source: oauth, signup or owner.
   */
  signed_up_by_source: Record<string, number>;
  /**
   * Customers with billable units in the month.
   */
  active: number;
  /**
   * Customers whose month amounts to more than zero.
   */
  paying: number;
  closed: number;
  requests: number;
  units: number;
  revenue: Array<MCPSellerStatsAmount>;
};

