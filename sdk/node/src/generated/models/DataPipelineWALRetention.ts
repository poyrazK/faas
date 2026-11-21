/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Fresh source-specific WAL headroom. The remaining-time estimate uses sampled cluster WAL generation and does not guarantee when a slot will be invalidated.
 */
export type DataPipelineWALRetention = {
  limit_bytes: number;
  remaining_bytes: number;
  estimated_seconds_remaining?: number;
};

