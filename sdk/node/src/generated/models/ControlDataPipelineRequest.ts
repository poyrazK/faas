/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Optimistic revision fence and requested pipeline lifecycle action.
 */
export type ControlDataPipelineRequest = {
  expected_revision: number;
  action: 'pause' | 'resume' | 'resync' | 'verify' | 'delete';
};

