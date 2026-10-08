/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowTransition } from './OperationWorkflowTransition.js';
/**
 * App-declared read-only workflow step represented by an observed milestone. New declarations select the instance ID from the validated public milestone payload using the pinned JSON Pointer.
 */
export type OperationWorkflowStep = {
 allow_reconciliation?: boolean;
  workflow: string;
  title: string;
  /**
   * Explicit workflow contract version. Legacy definitions that omit it have effective version 1.
   */
  version?: number;
  /**
   * App-declared business state vocabulary pinned with this workflow mapping.
   */
  states?: Array<string>;
  /**
   * App-declared terminal states, which must be in states and cannot have outgoing transitions.
   */
  terminal_states?: Array<string>;
  /**
   * App-declared age thresholds in seconds for active states. Keys must be states and cannot be terminal states.
   */
  state_stale_after_seconds?: Record<string, number>;
  /**
   * App-declared allowed state edges pinned with this workflow mapping.
   */
  transitions?: Array<OperationWorkflowTransition>;
  /**
   * True when the workflow declares transitions, including when this Operation has no scoped edges.
   */
  transitions_declared?: boolean;
  step: string;
  label: string;
  milestone: string;
  /**
   * JSON Pointer to a stable workflow-run ID in this milestone's payload; omitted by older pinned definitions.
   */
  instance_id_from?: string;
  /**
   * App-provided workflow-run ID selected from this observed milestone.
   */
  instance_id?: string;
  position: number;
};

