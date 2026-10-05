/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe evidence explanation and an optional inspection destination.
 */
export type AppHealthCheck = {
  code: 'workload' | 'deployment' | 'latest_deployment' | 'readiness' | 'traffic_readiness' | 'maintenance' | 'requests';
  status: 'pass' | 'warning' | 'fail' | 'unknown' | 'not_applicable';
  /**
   * Safe explanation without raw backend errors or probe output.
   */
  detail: string;
  action?: 'deployments' | 'configuration' | 'logs' | 'metrics' | 'errors';
  deployment_id?: string;
};

