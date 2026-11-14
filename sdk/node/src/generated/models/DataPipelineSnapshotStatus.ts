/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Most recent snapshot attempt plus the last activated capture. Rows and bytes describe that successful capture and remain unchanged after failed refreshes. A private target receipt preserves capture time through lost acknowledgements.
 */
export type DataPipelineSnapshotStatus = {
  run_id: string;
  state: 'due' | 'running' | 'succeeded' | 'failed';
  reason?: string;
  started_at?: string;
  next_run_at?: string;
  captured_at?: string;
  rows: number;
  bytes: number;
};

