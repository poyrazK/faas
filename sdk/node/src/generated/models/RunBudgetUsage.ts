/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Active reservations and metered usage in the current UTC-day window.
 */
export type RunBudgetUsage = {
  day: string;
  active: number;
  invocations: number;
  compute_used_mb_ms: number;
  compute_reserved_mb_ms: number;
};

