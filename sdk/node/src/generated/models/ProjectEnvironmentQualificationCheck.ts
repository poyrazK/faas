/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentQualificationResult } from './ProjectEnvironmentQualificationResult.js';
/**
 * Aggregate result plus one exact-deployment probe outcome per workload.
 */
export type ProjectEnvironmentQualificationCheck = {
  name: 'health' | 'smoke';
  status: 'passed' | 'failed';
  results: Array<ProjectEnvironmentQualificationResult>;
};

