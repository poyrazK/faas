/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InsightRequestVariant } from './InsightRequestVariant.js';
/**
 * Request telemetry by flag value.
 */
export type InsightRequests = {
  flag: string;
  from: string;
  until: string;
  variants: Array<InsightRequestVariant>;
};

