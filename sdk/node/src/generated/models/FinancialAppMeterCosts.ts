/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialAppCostAllocation } from './FinancialAppCostAllocation.js';
import type { FinancialMeterCoverage } from './FinancialMeterCoverage.js';
/**
 * Application-attributed meter costs with source coverage and workload allocations.
 */
export type FinancialAppMeterCosts = {
  meter: string;
  coverage: FinancialMeterCoverage;
  unit?: string;
  quantity: number;
  net_millicents: number;
  allocations: Array<FinancialAppCostAllocation>;
};

