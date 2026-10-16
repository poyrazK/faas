/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Public deployment readiness finding with its status, observed limits and suggested remedy.
 */
export type ProjectDeploymentCheck = {
  code: string;
  status: 'passed' | 'blocked' | 'unknown';
  workload?: string;
  message: string;
  remedy?: string;
  observed?: number;
  limit?: number;
};

