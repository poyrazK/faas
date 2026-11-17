/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeBuilderEvidenceReason } from './ObsRuntimeBuilderEvidenceReason.js';
import type { ObsRuntimeBuilderQueueValues } from './ObsRuntimeBuilderQueueValues.js';
/**
 * Notification hints local to this process, excluding durable fleet backlog. Waiting plus active may reach capacity plus workers because of dispatch handoffs. Observed_at is the oldest original clock among the queue's required gauges.
 */
export type ObsRuntimeBuilderQueue = {
  state: 'available' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeBuilderEvidenceReason;
  observed_at?: string;
  queue: 'build' | 'cancel';
  values?: ObsRuntimeBuilderQueueValues;
};

