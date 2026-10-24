/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialAllocation } from './FinancialAllocation.js';
import type { FinancialPrice } from './FinancialPrice.js';
/**
 * Meter cost priced by one immutable contract with its assigned allowance.
 */
export type FinancialMeterCost = {
  price: FinancialPrice;
  quantity: number;
  included_quantity: number;
  gross_millicents: number;
  allowance_millicents: number;
  net_millicents: number;
  allocation_method: string;
  allocations: Array<FinancialAllocation>;
};

