/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One selected region and its positive steady concurrency reservation.
 */
export type GlobalRuntimeQuotaShare = {
  region: string;
  /**
   * Positive steady concurrency share from the tenant plan.
   */
  concurrency: number;
};

