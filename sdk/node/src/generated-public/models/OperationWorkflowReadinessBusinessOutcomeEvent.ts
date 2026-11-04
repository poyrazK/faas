/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessBusinessOutcome } from './OperationWorkflowReadinessBusinessOutcome.js';
import type { OperationWorkflowReadinessOutcomeEscalationReference } from './OperationWorkflowReadinessOutcomeEscalationReference.js';
import type { OperationWorkflowReadinessSnapshotEntry } from './OperationWorkflowReadinessSnapshotEntry.js';
/**
 * Self-contained observed business result or deadline notice for a source completion. Reports never establish causation, business failure or execution authority. Source links may expire from inbox retention.
 */
export type OperationWorkflowReadinessBusinessOutcomeEvent = {
  escalation?: OperationWorkflowReadinessOutcomeEscalationReference;
  /**
   * OperationWorkflowReadinessBusinessOutcomeEvent.watch_id. Retained advisory business outcome watch metadata; does not modify application reports or coordination revisions.
   */
  watch_id: string;
  /**
   * OperationWorkflowReadinessBusinessOutcomeEvent.source_notification_id. Retained advisory business outcome watch metadata; does not modify application reports or coordination revisions.
   */
  source_notification_id: string;
  /**
   * OperationWorkflowReadinessBusinessOutcomeEvent.source_sequence. Retained advisory business outcome watch metadata; does not modify application reports or coordination revisions.
   */
  source_sequence: number;
  source: OperationWorkflowReadinessSnapshotEntry;
  /**
   * OperationWorkflowReadinessBusinessOutcomeEvent.previous_status. Retained advisory business outcome watch metadata; does not modify application reports or coordination revisions.
   */
  previous_status: 'achieved' | 'pending' | 'different_outcome' | 'unknown';
  /**
   * OperationWorkflowReadinessBusinessOutcomeEvent.deadline_at. Retained advisory business outcome watch metadata; does not modify application reports or coordination revisions.
   */
  deadline_at?: string;
  current: OperationWorkflowReadinessBusinessOutcome;
};

