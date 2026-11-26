/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InsightEventSeries } from './InsightEventSeries.js';
import type { InsightFunnel } from './InsightFunnel.js';
import type { InsightRetention } from './InsightRetention.js';
/**
 * One run of a saved insight.
 */
export type SavedInsightResult = {
  key: string;
  kind: 'events' | 'funnel' | 'retention';
  /**
   * Absent when there is no data: a funnel without entrants, or retention before a period has ended.
   */
  value?: number;
  from: string;
  until: string;
  events?: InsightEventSeries;
  funnel?: InsightFunnel;
  retention?: InsightRetention;
};

