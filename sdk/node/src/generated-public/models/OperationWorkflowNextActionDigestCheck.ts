/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowNextActionDigestCheckItem } from './OperationWorkflowNextActionDigestCheckItem.js';
/**
 * Read-only current source reconciliation of a saved digest, using independent retained source reads. No delivery snapshot or workflow is modified.
 */
export type OperationWorkflowNextActionDigestCheck = {
  items: Array<OperationWorkflowNextActionDigestCheckItem>;
  retention_limited: boolean;
};

