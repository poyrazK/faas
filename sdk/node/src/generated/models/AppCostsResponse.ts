/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppCostMeter } from './AppCostMeter.js';
/**
 * Retained application usage costs with coverage limitations and no account financial totals.
 */
export type AppCostsResponse = {
  app_id: string;
  app_slug: string;
  currency: string;
  period_start: string;
  period_end: string;
  as_of: string;
  known_usage_millicents: number;
  meters: Array<AppCostMeter>;
  scope: string;
  scope_description: string;
  bill_estimate_available: boolean;
  missing_bill_components: Array<string>;
};

