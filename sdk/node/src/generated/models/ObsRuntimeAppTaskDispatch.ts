/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeAppTaskDispatchValues } from './ObsRuntimeAppTaskDispatchValues.js';
import type { ObsRuntimeHandoffEvidenceReason } from './ObsRuntimeHandoffEvidenceReason.js';
/**
 * Local dispatcher activity independent of durable task collection. Disabled means opted out; stopped means dispatch enabled but runner inactive. Running does not prove progress. Slot occupancy includes restore, execution and teardown, not fleet tasks or VMs. Busy handlers need not claim work. No new stall threshold is inferred.
 */
export type ObsRuntimeAppTaskDispatch = {
  state: 'disabled' | 'running' | 'stopped' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeHandoffEvidenceReason;
  observed_at?: string;
  values?: ObsRuntimeAppTaskDispatchValues;
};

