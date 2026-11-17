/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InsightEventPoint } from './InsightEventPoint.js';
/**
 * A product analytics event series.
 */
export type InsightEventSeries = {
  event: string;
  unit: 'customer' | 'subject';
  interval: 'hour' | 'day';
  by?: string;
  from: string;
  until: string;
  points: Array<InsightEventPoint>;
};

