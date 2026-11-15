/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineQualityResult } from './DataPipelineQualityResult.js';
/**
 * Last completed or attempted quality scan for the recorded generation. Unknown and inconclusive are separate from passed; failing row values never appear in this observation.
 */
export type DataPipelineQualityStatus = {
  state: 'passed' | 'failed' | 'inconclusive' | 'unknown';
  checked_at: string;
  next_run_at: string;
  generation: number;
  results: Array<DataPipelineQualityResult>;
};

