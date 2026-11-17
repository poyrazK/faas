/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeHandoffCounts } from './ObsRuntimeHandoffCounts.js';
import type { ObsRuntimeHandoffItem } from './ObsRuntimeHandoffItem.js';
/**
 * Advisory build-to-image handoff evidence for selected image workers in v2 profiles. Replica recovery scopes can overlap; pending values are never summed into a fleet total. Positive waiting evidence survives incomplete recovery metadata.
 */
export type ObsRuntimeHandoffHealth = {
  state: 'waiting' | 'empty' | 'incomplete' | 'unknown';
  reason?: 'pending_handoffs_observed' | 'incomplete_handoff_evidence' | 'handoff_evidence_unavailable';
  counts: ObsRuntimeHandoffCounts;
  incomplete: boolean;
  items: Array<ObsRuntimeHandoffItem>;
  omitted_items: number;
};

