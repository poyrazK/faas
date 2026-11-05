/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialPrice } from './FinancialPrice.js';
/**
 * Historical activation of recorded account pricing.
 */
export type FinancialPriceContract = {
  price: FinancialPrice;
  plan: 'free' | 'hobby' | 'pro' | 'scale';
  effective_from: string;
  delivery_mode: 'live' | 'shadow' | 'off';
};

