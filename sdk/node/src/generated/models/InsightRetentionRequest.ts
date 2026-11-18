/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InsightStepRequest } from './InsightStepRequest.js';
/**
 * A retention query.
 */
export type InsightRetentionRequest = {
  start: InsightStepRequest;
  return: InsightStepRequest;
  from?: string;
  until?: string;
  period?: 'day' | 'week';
  periods?: number;
  unit?: 'customer' | 'subject';
  /**
   * Split cohorts by flag:KEY or property:KEY.
   */
  by?: string;
};

