/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeBuilderQueue } from './ObsRuntimeBuilderQueue.js';
import type { ObsRuntimeBuilderSlots } from './ObsRuntimeBuilderSlots.js';
import type { ObsRuntimeBuilderWorker } from './ObsRuntimeBuilderWorker.js';
/**
 * Sanitized observations of one selected builder target, with independently validated worker, slot and notification queue components.
 */
export type ObsRuntimeBuilderItem = {
  node?: string;
  target_id?: string;
  unexpected?: boolean;
  scrape_observed_at?: string;
  state: 'available' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  worker: ObsRuntimeBuilderWorker;
  slots: ObsRuntimeBuilderSlots;
  /**
   * Exactly the build and cancel queues, in that order.
   */
  queues: Array<ObsRuntimeBuilderQueue>;
};

