/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineQualityStatus } from './DataPipelineQualityStatus.js';
import type { DataPipelineSnapshotStatus } from './DataPipelineSnapshotStatus.js';
/**
 * Historical scheduled-work observations. Capture time bounds data age independently of the catalog worker observation timestamp.
 */
export type DataPipelineOperationsStatus = {
  snapshot?: DataPipelineSnapshotStatus;
  quality?: DataPipelineQualityStatus;
};

