/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The request budget a tenant plan sets (ADR-240); zero disables a ceiling.
 */
export type PlatformTenantPlanBudget = {
  max_requests_per_minute: number;
  max_requests_per_day: number;
};

