/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryAutomaticQueueEntry } from './WorkflowRecoveryAutomaticQueueEntry.js';
/**
 * Retained automatic queue entry and the effect of replacing its policy configuration.
 */
export type WorkflowRecoveryPolicyChangeEntry = {
  entry: WorkflowRecoveryAutomaticQueueEntry;
  effect: 'would_suppress' | 'retain_slot';
};

