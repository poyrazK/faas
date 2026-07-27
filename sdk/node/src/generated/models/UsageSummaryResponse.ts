/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Account-level monthly roll-up: included GB-hours, used, overage math, and remaining balance.
 */
export type UsageSummaryResponse = {
  month: string;
  used_gb_hours: number;
  included_gb_hours: number;
  overage_gb_hours: number;
  /**
   * Integer cents. Overages are €0.01/GB-h.
   */
  overage_cents: number;
};

