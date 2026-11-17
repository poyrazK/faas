/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeBuilderEvidenceReason } from './ObsRuntimeBuilderEvidenceReason.js';
import type { ObsRuntimeBuilderWorkerValues } from './ObsRuntimeBuilderWorkerValues.js';
/**
 * Waiting/working/draining/overdue describe usable sampled poller evidence, not successful builds. Observed_at is the oldest original sample clock among this component's required gauges. Invalid or incomplete components omit values.
 */
export type ObsRuntimeBuilderWorker = {
  state: 'waiting' | 'working' | 'draining' | 'overdue' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeBuilderEvidenceReason;
  observed_at?: string;
  values?: ObsRuntimeBuilderWorkerValues;
};

