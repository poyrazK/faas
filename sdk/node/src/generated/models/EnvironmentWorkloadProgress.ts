/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-only progress for one exact reviewed graph member. Serving remains unknown without current whole-graph convergence proof.
 */
export type EnvironmentWorkloadProgress = {
  resource: string;
  app_id: string;
  deployment_id?: string;
  job_id?: string;
  execution_mode: string;
  build_status?: string;
  preparation: 'complete' | 'pending' | 'failed' | 'unknown';
  qualification: 'complete' | 'pending' | 'failed' | 'unknown';
  activation: 'complete' | 'pending' | 'failed' | 'unknown';
  serving: 'complete' | 'pending' | 'failed' | 'unknown';
  blocking_reasons: Array<string>;
};

