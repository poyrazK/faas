/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineQualityRule } from './DataPipelineQualityRule.js';
/**
 * Scheduled curated mirror checks, limited to 16 named rules and 10000 inspected source rows per rule. Incomplete coverage cannot be reported as passed; only counts and status leave the private worker.
 */
export type DataPipelineQualitySchedule = {
  every_seconds: number;
  rules: Array<DataPipelineQualityRule>;
};

