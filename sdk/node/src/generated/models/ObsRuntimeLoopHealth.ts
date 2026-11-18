/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeLoopCounts } from './ObsRuntimeLoopCounts.js';
import type { ObsRuntimeLoopItem } from './ObsRuntimeLoopItem.js';
/**
 * Advisory reports from expected runtime and work loops; fresh reported stalls remain visible when age evidence is incomplete. Counts cover all inspected target-loop pairs before the bounded preview.
 */
export type ObsRuntimeLoopHealth = {
  state: 'stalled' | 'no_reported_stall' | 'incomplete' | 'unknown';
  reason?: 'reported_loop_stall' | 'incomplete_loop_evidence' | 'loop_evidence_unavailable';
  counts: ObsRuntimeLoopCounts;
  incomplete: boolean;
  items: Array<ObsRuntimeLoopItem>;
  omitted_items: number;
};

