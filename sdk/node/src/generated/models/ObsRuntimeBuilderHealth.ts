/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeBuilderCounts } from './ObsRuntimeBuilderCounts.js';
import type { ObsRuntimeBuilderItem } from './ObsRuntimeBuilderItem.js';
/**
 * Advisory build worker progress and process-local queue/slot pressure for explicit v2 profiles. Counts cover every inspected target before previewing. A fresh overdue worker remains visible with incomplete queue/slot evidence; saturation does not establish a build failure.
 */
export type ObsRuntimeBuilderHealth = {
  state: 'observed' | 'overdue' | 'incomplete' | 'unknown';
  reason?: 'worker_progress_overdue' | 'incomplete_builder_evidence' | 'builder_evidence_unavailable';
  counts: ObsRuntimeBuilderCounts;
  incomplete: boolean;
  items: Array<ObsRuntimeBuilderItem>;
  omitted_items: number;
};

