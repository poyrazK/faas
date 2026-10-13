/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowNextActionQueueItem } from './OperationWorkflowNextActionQueueItem.js';
import type { OperationWorkflowReadinessCompletion } from './OperationWorkflowReadinessCompletion.js';
/**
 * Current result for one saved source action. Completed requires a retained explicit resolved source follow-up closure; no-longer-outstanding and unavailable never imply completion. Current owner filters are intentionally not applied so reassignment remains visible. Linked evidence health and application success are not inferred.
 */
export type OperationWorkflowNextActionDigestCheckItem = {
  subscription_id: string;
  sequence: number;
  action_kind: string;
  status: 'outstanding' | 'changed' | 'completed' | 'no_longer_outstanding' | 'unavailable';
  explanation: string;
  changed_fields: Array<string>;
  current?: OperationWorkflowNextActionQueueItem;
  completion?: OperationWorkflowReadinessCompletion;
};

