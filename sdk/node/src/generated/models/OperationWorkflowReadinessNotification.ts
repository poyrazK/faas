/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessChange } from './OperationWorkflowReadinessChange.js';
import type { OperationWorkflowReadinessCoordination } from './OperationWorkflowReadinessCoordination.js';
import type { OperationWorkflowReadinessDueReminder } from './OperationWorkflowReadinessDueReminder.js';
/**
 * Durable advisory readiness notification containing a bounded comparison and reporting Operation identities.
 */
export type OperationWorkflowReadinessNotification = {
  coordination?: OperationWorkflowReadinessCoordination;
  /**
   * OperationWorkflowReadinessNotification.id. Stable notification UUID.
   */
  id: string;
  /**
   * OperationWorkflowReadinessNotification.sequence. Monotonic subscription-local delivery cursor.
   */
  sequence: number;
  /**
   * OperationWorkflowReadinessNotification.kind. Matched subscription notification kind.
   */
  kind: 'newly_ready' | 'newly_blocked' | 'reassigned' | 'unavailable';
  /**
   * OperationWorkflowReadinessNotification.created_at. Time the observation was converted into a notification.
   */
  created_at: string;
  /**
   * OperationWorkflowReadinessNotification.explanation. Human-readable change or unavailable-evaluation explanation.
   */
  explanation: string;
  change?: OperationWorkflowReadinessChange;
  reminder?: OperationWorkflowReadinessDueReminder;
};

