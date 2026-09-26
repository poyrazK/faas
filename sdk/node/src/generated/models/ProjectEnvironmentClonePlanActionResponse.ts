/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Non-secret summary of how one resource behaves during a clone.
 */
export type ProjectEnvironmentClonePlanActionResponse = {
  workload_slug?: string;
  resource: string;
  action: 'copy' | 'copy_sealed' | 'isolate' | 'share' | 'reuse' | 'promote' | 'not_available' | 'shared' | 'skip';
  count?: number;
  reason?: string;
};

