/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One day in the account's trailing 30 UTC calendar day usage trend (issue #308).
 */
export type DailyUsagePoint = {
  /**
   * UTC calendar day.
   */
  date: string;
  /**
   * Account-wide GB-hours consumed on this day. Informational; uses the usage summary conversion.
   */
  gb_hours: number;
  /**
   * Slug of the app contributing the most GB-hours on this day.
   */
  top_app_slug?: string;
  /**
   * GB-hours consumed by top_app_slug on this day.
   */
  top_app_gb_hours?: number;
};

