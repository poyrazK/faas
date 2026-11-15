/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One curated rule outcome. A lower-bound count reflects limited coverage; zero violations with incomplete coverage is inconclusive rather than passed.
 */
export type DataPipelineQualityResult = {
  name: string;
  state: 'passed' | 'failed' | 'inconclusive' | 'unknown';
  violations: number;
  count_is_lower_bound: boolean;
};

