/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Non-secret GET probe result tied to one workload deployment in the active release set.
 */
export type ProjectEnvironmentQualificationResult = {
  workload_slug: string;
  deployment_id: string;
  status: 'passed' | 'failed';
  http_status?: number;
  error_code?: 'preview_unavailable' | 'request_failed' | 'unexpected_status';
};

