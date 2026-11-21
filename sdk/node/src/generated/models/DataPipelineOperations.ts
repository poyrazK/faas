/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineQualitySchedule } from './DataPipelineQualitySchedule.js';
import type { DataPipelineSnapshotSchedule } from './DataPipelineSnapshotSchedule.js';
import type { DataPipelineWorkload } from './DataPipelineWorkload.js';
/**
 * Versioned policy for bounded scheduled snapshots, mirror quality checks and optional maintenance. Snapshot mode is chosen at creation and cannot be changed later.
 */
export type DataPipelineOperations = {
  version: 1;
  snapshot?: DataPipelineSnapshotSchedule;
  quality?: DataPipelineQualitySchedule;
  workload?: DataPipelineWorkload;
};

