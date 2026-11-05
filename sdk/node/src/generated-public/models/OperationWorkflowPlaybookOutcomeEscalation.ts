/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookBusinessOutcome } from './OperationWorkflowPlaybookBusinessOutcome.js';
import type { OperationWorkflowReadinessEscalationResponse } from './OperationWorkflowReadinessEscalationResponse.js';
/**
 * OperationWorkflowPlaybookOutcomeEscalation. Retained assigned deadline follow-up evidence. Polling refreshes current observations and the first observed success only; acknowledgment, owner, notes, due times, reminders and completion remain human coordination.
 */
export type OperationWorkflowPlaybookOutcomeEscalation = {
  response?: OperationWorkflowReadinessEscalationResponse;
  /**
   * OperationWorkflowPlaybookOutcomeEscalation.watch_id. Exact durable watch identity for fenced evidence refresh.
   */
  watch_id: string;
  /**
   * OperationWorkflowPlaybookOutcomeEscalation.progress_id. Exact pinned checklist that originally missed its observed deadline.
   */
  progress_id: string;
  /**
   * OperationWorkflowPlaybookOutcomeEscalation.deadline_at. Original outcome deadline, unchanged after delivery.
   */
  deadline_at: string;
  /**
   * OperationWorkflowPlaybookOutcomeEscalation.current. Latest accepted watch evidence, independently of the original notice and human disposition.
   */
  current: OperationWorkflowPlaybookBusinessOutcome;
  /**
   * OperationWorkflowPlaybookOutcomeEscalation.success_observed_at. First subsequent achieved observation, retained through corrections or evidence loss; does not prove when the business result occurred.
   */
  success_observed_at?: string;
};

