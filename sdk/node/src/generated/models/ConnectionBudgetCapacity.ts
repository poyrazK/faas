/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Qualified tenant-usable allocation for the exact database generation and placement. Current adapters leave this unknown.
 */
export type ConnectionBudgetCapacity = {
  state: 'unknown' | 'verified';
  usable_connections?: number;
};

