/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppCostCoverage } from './AppCostCoverage.js';
import type { FinancialAppCostAllocation } from './FinancialAppCostAllocation.js';
/**
 * Exact retained quantity and cost allocations for one app and meter.
 */
export type AppCostMeter = {
  meter: string;
  coverage: AppCostCoverage;
  unit?: string;
  quantity: number;
  net_millicents: number;
  allocations: Array<FinancialAppCostAllocation>;
};

