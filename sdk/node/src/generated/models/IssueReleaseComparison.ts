/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueReleaseHealth } from './IssueReleaseHealth.js';
/**
 * Baseline and candidate deployment health with newly observed, recurring and resolved issue groups.
 */
export type IssueReleaseComparison = {
  window_start: string;
  window_end: string;
  environment: string;
  baseline: IssueReleaseHealth;
  candidate: IssueReleaseHealth;
  /**
   * Candidate minus baseline observed HTTP 5xx rate, expressed as a fraction. Null if either has no observed requests.
   */
  http_5xx_rate_delta: number | null;
};

