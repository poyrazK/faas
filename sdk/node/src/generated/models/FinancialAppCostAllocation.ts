/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialAttribution } from './FinancialAttribution.js';
/**
 * Workload share of a meter cost attributed to one application.
 */
export type FinancialAppCostAllocation = {
  attribution: FinancialAttribution;
  unit: string;
  quantity: number;
  net_millicents: number;
};

