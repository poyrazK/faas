/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Fires when the saved insight's value compares to threshold; funnel and retention values are fractions.
 */
export type SavedInsightAlert = {
  comparison: 'gt' | 'gte' | 'lt' | 'lte';
  threshold: number;
};

