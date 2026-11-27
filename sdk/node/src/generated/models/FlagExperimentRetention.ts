/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InsightStepRequest } from './InsightStepRequest.js';
/**
 * A retention metric: a customer converts when it sends the return event in period N after its first exposure.
 */
export type FlagExperimentRetention = {
  return: InsightStepRequest;
  period: 'day' | 'week';
  /**
   * The period after exposure that counts as returning.
   */
  period_number: number;
};

