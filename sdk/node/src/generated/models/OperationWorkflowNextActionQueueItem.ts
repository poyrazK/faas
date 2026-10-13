/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowCaseNextAction } from './OperationWorkflowCaseNextAction.js';
import type { OperationWorkflowNextActionCase } from './OperationWorkflowNextActionCase.js';
/**
 * Current source action shown once across its matching workflow cases. Owner and due filters apply to the action, not parent coordination. Shared record controls affect all covered cases; evidence and case lists respect the customer/workflow selector.
 */
export type OperationWorkflowNextActionQueueItem = {
  action: OperationWorkflowCaseNextAction;
  cases: Array<OperationWorkflowNextActionCase>;
  scope: string;
  /**
   * Source subscription customer selector for opening its controls, distinct from affected case customers.
   */
  subscription_tenant_id?: string;
};

