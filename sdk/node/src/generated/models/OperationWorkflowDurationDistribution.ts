/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One accumulated duration per eligible workflow. Nearest-rank percentiles are zero when workflow_count is zero.
 */
export type OperationWorkflowDurationDistribution = {
  workflow_count: number;
  total_seconds: number;
  p50_seconds: number;
  p95_seconds: number;
};

