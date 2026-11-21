/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeHandoffBacklog } from './ObsRuntimeHandoffBacklog.js';
import type { ObsRuntimeHandoffCollection } from './ObsRuntimeHandoffCollection.js';
import type { ObsRuntimeHandoffRecovery } from './ObsRuntimeHandoffRecovery.js';
/**
 * One sanitized image worker's collection, pending-work snapshot and recovery activity. Each component is independently validated against the same original scrape target.
 */
export type ObsRuntimeHandoffItem = {
  node?: string;
  target_id?: string;
  unexpected?: boolean;
  scrape_observed_at?: string;
  state: 'available' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  collection: ObsRuntimeHandoffCollection;
  backlog: ObsRuntimeHandoffBacklog;
  recovery: ObsRuntimeHandoffRecovery;
};

