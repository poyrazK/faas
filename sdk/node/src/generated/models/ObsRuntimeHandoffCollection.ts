/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeHandoffCollectionValues } from './ObsRuntimeHandoffCollectionValues.js';
import type { ObsRuntimeHandoffEvidenceReason } from './ObsRuntimeHandoffEvidenceReason.js';
/**
 * Freshness and shape of the collection-status gauges. Available metadata can report unsuccessful or expired collection; that never establishes an empty backlog.
 */
export type ObsRuntimeHandoffCollection = {
  state: 'available' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeHandoffEvidenceReason;
  observed_at?: string;
  values?: ObsRuntimeHandoffCollectionValues;
};

