/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An open customer at 80% or more of its plan's monthly units this month.
 */
export type MCPSellerNearLimit = {
  consumer_id: string;
  name: string;
  plan: string;
  units: number;
  max_units_per_month: number;
  percent: number;
};

