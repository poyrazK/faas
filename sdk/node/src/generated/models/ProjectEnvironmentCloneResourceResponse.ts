/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Progress and named blockers for an individual resource in a complete environment clone.
 */
export type ProjectEnvironmentCloneResourceResponse = {
  kind: string;
  name: string;
  source_id?: string;
  source_version?: string;
  target_id?: string;
  capture_point?: string;
  status: 'planned' | 'capturing' | 'captured' | 'copying' | 'verifying' | 'ready' | 'failed' | 'unsupported' | 'compensating' | 'compensated';
};

