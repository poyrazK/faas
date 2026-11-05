/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Dependency on another workload in the same deployment.
 */
export type WorkloadDependency = {
  /**
   * Workload name: main or another sidecar name.
   */
  name: string;
  /**
   * Lifecycle condition required before the dependent workload starts.
   */
  condition?: 'started' | 'healthy' | 'completed_successfully';
};

