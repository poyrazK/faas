/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * ADR-202 push body. The metric name travels in the URL path, so the body carries only the number that varies — which is what makes the push idempotent by construction.
 */
export type CustomMetricRequest = {
  /**
   * Fleet-total quantity one instance should carry. The scheduler computes ceil(value / target). Must be finite, >= 0, and <= 1e12; a larger reading is a broken producer, and without the bound one bad push would demand the plan cap's worth of instances on the very next tick.
   */
  value: number;
};

