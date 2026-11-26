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
   * The current month priced so far.
   */
  estimated_millicents: number;
};

