/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowStepSpec } from './WorkflowStepSpec.js';
import type { WorkflowTriggerSpec } from './WorkflowTriggerSpec.js';
/**
 * A named workflow DAG submitted with a deployment (ADR-081). max_concurrent_runs caps active run instances for this workflow; excess admitted runs remain pending until a slot opens, subject to the app plan's run quota. max_concurrent_actions caps active executor steps across runs of this workflow; steps wait in the scheduler queue while all action slots are occupied.
 */
export type WorkflowSpec = {
  name: string;
  trigger?: (WorkflowTriggerSpec | null);
  /**
   * Maximum active run instances for this workflow. Omit or set 0 to rely only on the app plan limit. Pending runs that have not started are queued and do not consume a slot.
   */
  max_concurrent_runs?: number;
  /**
   * Maximum running action and condition-check steps across runs of this workflow. Omit or set 0 for no additional per-workflow action cap. Event, callback, and duration waits do not consume an action slot. Runs use the value from their immutable definition snapshot.
   */
  max_concurrent_actions?: number;
  steps: Array<WorkflowStepSpec>;
};

