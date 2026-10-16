/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingVerificationRunSummary } from './BindingVerificationRunSummary.js';
/**
 * Bounded retained receipt summaries and a cursor tied to the original observation cutoff.
 */
export type BindingVerificationRunList = {
  runs: Array<BindingVerificationRunSummary>;
  observed_at: string;
  next_cursor?: string;
};

