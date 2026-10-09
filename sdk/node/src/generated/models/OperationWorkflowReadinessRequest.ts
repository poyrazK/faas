/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowPlannedDecision } from './OperationWorkflowPlannedDecision.js';
import type { OperationWorkflowPlannedEffect } from './OperationWorkflowPlannedEffect.js';
import type { OperationWorkflowPlannedInvariant } from './OperationWorkflowPlannedInvariant.js';
/**
 * Proposed workflow edge and evidence plan to evaluate against retained reports without executing it.
 */
export type OperationWorkflowReadinessRequest = {
  effects?: Array<OperationWorkflowPlannedEffect>;
  invariants?: Array<OperationWorkflowPlannedInvariant>;
  decisions?: Array<OperationWorkflowPlannedDecision>;
  /**
   * For this proposed transition, required in customer-self mode only.
   */
  app_id?: string;
  /**
   * For this proposed transition, required in account mode only.
   */
  tenant_id?: string;
  scope: string;
  subject: OperationSubject;
  workflow: string;
  instance_id: string;
  operation: string;
  from_state: string;
  to_state: string;
  /**
   * Planned milestone names for this transition; retained historical facts are not evidence for the new transaction.
   */
  milestones?: Array<string>;
  /**
   * For this proposed transition, optional expected retained source revision.
   */
  state_revision?: number;
  /**
   * For this proposed transition, optional expected selected contract version.
   */
  contract_version?: number;
};

