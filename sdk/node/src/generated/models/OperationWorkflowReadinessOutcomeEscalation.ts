/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessBusinessOutcome } from './OperationWorkflowReadinessBusinessOutcome.js';
import type { OperationWorkflowReadinessEscalationResponse } from './OperationWorkflowReadinessEscalationResponse.js';
/**
 * Live evidence for an assigned outcome escalation, separate from immutable deadline delivery evidence and human coordination. First observed application success is retained as a flag; it never automatically acknowledges or completes work. Monitoring ends with source retention.
 */
export type OperationWorkflowReadinessOutcomeEscalation = {
  response?: OperationWorkflowReadinessEscalationResponse;
  /**
   * OperationWorkflowReadinessOutcomeEscalation.watch_id. Retained coordination escalation policy or evidence; business interpretation and execution remain application-owned.
   */
  watch_id: string;
  /**
   * OperationWorkflowReadinessOutcomeEscalation.source_notification_id. Retained coordination escalation policy or evidence; business interpretation and execution remain application-owned.
   */
  source_notification_id: string;
  /**
   * OperationWorkflowReadinessOutcomeEscalation.source_sequence. Retained coordination escalation policy or evidence; business interpretation and execution remain application-owned.
   */
  source_sequence: number;
  current: OperationWorkflowReadinessBusinessOutcome;
  /**
   * OperationWorkflowReadinessOutcomeEscalation.success_observed_at. Retained coordination escalation policy or evidence; business interpretation and execution remain application-owned.
   */
  success_observed_at?: string;
};

