/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowBlockerEscalationPolicy } from './OperationWorkflowBlockerEscalationPolicy.js';
import type { OperationWorkflowTransition } from './OperationWorkflowTransition.js';
/**
 * App-declared read-only workflow step represented by an observed milestone. New declarations select the instance ID from the validated public milestone payload using the pinned JSON Pointer.
 */
export type OperationWorkflowStep = {
  /**
   * Versioned policies by blocker code. All steps in one workflow definition must agree. Policies only recommend escalation for active workflows with known blocker age.
   */
  blocker_escalations?: Record<string, OperationWorkflowBlockerEscalationPolicy>;
  /**
   * Explicit permission for evidence-backed state reconciliation snapshots.
   */
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
   * Optional whole-percent warning thresholds for states with SLA budgets. At the threshold a known current visit becomes at_risk until its budget is breached. Omitted states have no early warning. All steps must agree; publish a new workflow contract version when changing thresholds.
   */
  state_sla_warning_percent?: Record<string, number>;
  /**
   * Optional observed state visit budgets. Keys must be declared nonterminal states. All workflow steps in one definition must agree; publish a new workflow contract version for budget changes. Metadata updates do not reset a visit clock. Unknown retained entry times do not imply a breach.
   */
  state_sla_budget_seconds?: Record<string, number>;
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

