/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowPlannedDecision } from './OperationWorkflowPlannedDecision.js';
import type { OperationWorkflowPlannedEffect } from './OperationWorkflowPlannedEffect.js';
import type { OperationWorkflowPlannedInvariant } from './OperationWorkflowPlannedInvariant.js';
export type OperationWorkflowReadinessRequest = {
  effects?: Array<OperationWorkflowPlannedEffect>;
  invariants?: Array<OperationWorkflowPlannedInvariant>;
  decisions?: Array<OperationWorkflowPlannedDecision>;
  /**
   * Required in customer-self mode only.
   */
  app_id?: string;
  /**
   * Required in account mode only.
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
   * Optional expected retained source revision.
   */
  state_revision?: number;
  /**
   * Optional expected selected contract version.
   */
  contract_version?: number;
};

