/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIUserUsageItem } from './OutboundAIUserUsageItem.js';
/**
 * One UTC day of per-end-user AI usage, most expensive first (ADR-994).
 */
export type OutboundAIUserUsageResponse = {
  usage_date: string;
  user_daily_token_limit: number | null;
  user_daily_cost_limit_millicents: number | null;
  /**
   * Distinct end-user rows of the day, including "~other".
   */
  tracked_users: number;
  items: Array<OutboundAIUserUsageItem>;
};

