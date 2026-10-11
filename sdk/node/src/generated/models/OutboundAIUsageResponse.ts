/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIUsageItem } from './OutboundAIUsageItem.js';
/**
 * One UTC day of AI gateway usage for an integration.
 */
export type OutboundAIUsageResponse = {
  usage_date: string;
  /**
   * Empty when AI handling is off.
   */
  ai_provider: string;
  /**
   * Settled admission counter for today; the rollup sum of input and output tokens for an earlier day.
   */
  daily_token_count: number;
  daily_token_limit: number | null;
  /**
   * End of usage_date in UTC.
   */
  resets_at: string;
  items: Array<OutboundAIUsageItem>;
  daily_cost_millicents?: number;
  daily_cost_limit_millicents?: number | null;
  /**
   * Calendar month of usage_date as YYYY-MM.
   */
  month?: string;
  /**
   * Settled cost of the month through usage_date.
   */
  monthly_cost_millicents?: number;
  monthly_cost_limit_millicents?: number | null;
};

