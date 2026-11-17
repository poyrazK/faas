/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeProcessCounts } from './ObsRuntimeProcessCounts.js';
/**
 * Advisory sampled process continuity at two evaluation times; an observed change does not prove a crash or count every restart between samples.
 */
export type ObsRuntimeProcess = {
  state: 'baseline' | 'changed' | 'unchanged' | 'incomplete' | 'unknown';
  reason?: 'awaiting_comparison' | 'observed_process_change' | 'incomplete_continuity_evidence' | 'process_evidence_unavailable';
  comparison_at?: string;
  counts: ObsRuntimeProcessCounts;
  incomplete: boolean;
};

