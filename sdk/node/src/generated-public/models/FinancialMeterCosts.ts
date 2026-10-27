/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialContractCosts } from './FinancialContractCosts.js';
import type { FinancialForecast } from './FinancialForecast.js';
import type { FinancialMeterCoverage } from './FinancialMeterCoverage.js';
import type { FinancialPriceContract } from './FinancialPriceContract.js';
/**
 * Accrued meter costs, source coverage, historical terms, and forecast.
 */
export type FinancialMeterCosts = {
  meter: string;
  coverage: FinancialMeterCoverage;
  accrued: FinancialContractCosts;
  forecast: FinancialForecast;
  price_contracts: Array<FinancialPriceContract>;
};

