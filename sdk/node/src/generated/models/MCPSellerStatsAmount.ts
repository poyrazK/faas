/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Revenue in one currency, in millicents.
 */
export type MCPSellerStatsAmount = {
  currency: string;
  /**
   * Finalized statements, before credits.
   */
  billed_millicents: number;
  /**
   * The current month priced so far; in a closed month, draft statements the month-end run has not finalized yet.
   */
  estimated_millicents: number;
};

