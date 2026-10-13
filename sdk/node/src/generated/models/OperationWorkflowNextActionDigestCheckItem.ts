/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowNextActionQueueItem } from './OperationWorkflowNextActionQueueItem.js';
import type { OperationWorkflowObligationEvent } from './OperationWorkflowObligationEvent.js';
import type { OperationWorkflowReadinessCompletion } from './OperationWorkflowReadinessCompletion.js';
/**
 * Current result for one saved source action. Completed requires an epoch-matched lifecycle receipt or an explicit resolved generic source follow-up closure; no-longer-outstanding and unavailable never imply completion. Current owner filters are intentionally not applied so reassignment remains visible. Linked evidence health and application success are not inferred.
 */
export type OperationWorkflowNextActionDigestCheckItem = {
  obligation_id?: string;
  /**
   * Lifecycle coverage is bounded by source retention and available anchored receipts; missing events never prove completion.
   */
  history_limited: boolean;
  lifecycle?: Array<OperationWorkflowObligationEvent>;
  subscription_id: string;
  sequence: number;
  action_kind: string;
  status: 'outstanding' | 'changed' | 'completed' | 'no_longer_outstanding' | 'unavailable' | 'replaced' | 'canceled';
  explanation: string;
  changed_fields: Array<string>;
  current?: OperationWorkflowNextActionQueueItem;
  completion?: OperationWorkflowReadinessCompletion;
};

