/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessVerificationWatchItem } from './OperationWorkflowReadinessVerificationWatchItem.js';
/**
 * A changed disposition of one captured resolution, delivered once for an observed status change.
 */
export type OperationWorkflowReadinessVerificationChange = {
  /**
   * OperationWorkflowReadinessVerificationChange.before_status. Server-managed advisory verification observation for the retained source completion.
   */
  before_status: 'awaiting_verification' | 'verified' | 'invalidated' | 'unavailable';
  after: OperationWorkflowReadinessVerificationWatchItem;
};

