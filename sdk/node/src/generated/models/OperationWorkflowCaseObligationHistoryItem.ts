/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowCaseNextAction } from './OperationWorkflowCaseNextAction.js';
import type { OperationWorkflowObligationEvent } from './OperationWorkflowObligationEvent.js';
import type { OperationWorkflowObligationFulfillmentReview } from './OperationWorkflowObligationFulfillmentReview.js';
/**
 * Case-scoped receipts grouped by exact source obligation, alongside its current action when still projected. Absence of a current action is not completion. Shared source controls can affect additional cases; foreign case identities are excluded.
 */
export type OperationWorkflowCaseObligationHistoryItem = {
  fulfillment_review?: OperationWorkflowObligationFulfillmentReview;
  obligation_id: string;
  action_kind: string;
  subscription_id: string;
  sequence: number;
  scope: string;
  subscription_tenant_id?: string;
  shared_plan: boolean;
  events: Array<OperationWorkflowObligationEvent>;
  events_truncated: boolean;
  current?: OperationWorkflowCaseNextAction;
};

