/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReconciliation } from './OperationWorkflowReconciliation.js';
/**
 * Application-reported discrepancy recorded as a declared milestone using the existing transaction, ownership, and retention boundaries.
 */
export type OperationWorkflowReconciliationPayload = {
  kind: 'gregale.workflow-reconciliation.v1';
  reconciliation: OperationWorkflowReconciliation;
};

