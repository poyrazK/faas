/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InsightFunnelBreakdown } from './InsightFunnelBreakdown.js';
import type { InsightFunnelStep } from './InsightFunnelStep.js';
/**
 * A funnel result.
 */
export type InsightFunnel = {
  unit: 'customer' | 'subject';
  window: string;
  by?: string;
  from: string;
  until: string;
  steps: Array<InsightFunnelStep>;
  breakdowns?: Array<InsightFunnelBreakdown>;
  /**
   * The query read AnalyticsQueryMaxUnits units and left the rest out.
   */
  truncated: boolean;
};

