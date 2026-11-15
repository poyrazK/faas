/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineOperations } from './DataPipelineOperations.js';
/**
 * Revision-checked replacement maintenance configuration or an explicit snapshot/quality run request. Configure supplies config; run actions omit it and still respect the saved UTC window.
 */
export type DataPipelineOperationsRequest = {
  expected_revision: number;
  action: 'configure' | 'run_snapshot' | 'run_quality';
  config?: DataPipelineOperations;
};

