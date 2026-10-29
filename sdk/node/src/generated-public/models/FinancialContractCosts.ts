/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialMeterCost } from './FinancialMeterCost.js';
/**
 * One shared period allowance applied across historical rate versions.
 */
export type FinancialContractCosts = {
  meter: string;
  quantity: number;
  included_quantity: number;
  net_millicents: number;
  allowance_method: string;
  contracts: Array<FinancialMeterCost>;
};

