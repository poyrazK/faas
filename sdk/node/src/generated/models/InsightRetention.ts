/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InsightRetentionRow } from './InsightRetentionRow.js';
/**
 * A retention result.
 */
export type InsightRetention = {
  unit: 'customer' | 'subject';
  period: 'day' | 'week';
  periods: number;
  by?: string;
  from: string;
  until: string;
  overall: InsightRetentionRow;
  cohorts: Array<InsightRetentionRow>;
  breakdowns?: Array<InsightRetentionRow>;
  truncated: boolean;
};

