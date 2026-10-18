/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAITagUsageItem } from './OutboundAITagUsageItem.js';
/**
 * One UTC day of AI usage by tag, most expensive first (ADR-1436).
 */
export type OutboundAITagUsageResponse = {
  usage_date: string;
  /**
   * Distinct tag rows of the day, overflow rows included.
   */
  tracked_tags: number;
  items: Array<OutboundAITagUsageItem>;
};

