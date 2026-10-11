/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessBusinessOutcomeEvent } from './OperationWorkflowReadinessBusinessOutcomeEvent.js';
import type { OperationWorkflowReadinessBusinessOutcomeWatch } from './OperationWorkflowReadinessBusinessOutcomeWatch.js';
import type { OperationWorkflowReadinessChange } from './OperationWorkflowReadinessChange.js';
import type { OperationWorkflowReadinessCoordination } from './OperationWorkflowReadinessCoordination.js';
import type { OperationWorkflowReadinessDueReminder } from './OperationWorkflowReadinessDueReminder.js';
import type { OperationWorkflowReadinessOutcomeEscalation } from './OperationWorkflowReadinessOutcomeEscalation.js';
import type { OperationWorkflowReadinessVerificationEvent } from './OperationWorkflowReadinessVerificationEvent.js';
import type { OperationWorkflowReadinessVerificationWatch } from './OperationWorkflowReadinessVerificationWatch.js';
/**
 * Durable advisory readiness notification containing a bounded comparison and reporting Operation identities.
 */
export type OperationWorkflowReadinessNotification = {
  outcome_escalation?: OperationWorkflowReadinessOutcomeEscalation;
  business_outcome_watch?: OperationWorkflowReadinessBusinessOutcomeWatch;
  business_outcome?: OperationWorkflowReadinessBusinessOutcomeEvent;
  verification_watch?: OperationWorkflowReadinessVerificationWatch;
  verification?: OperationWorkflowReadinessVerificationEvent;
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
  kind: 'newly_ready' | 'newly_blocked' | 'reassigned' | 'unavailable' | 'verification_verified' | 'verification_invalidated' | 'verification_unavailable' | 'outcome_achieved' | 'outcome_different_outcome' | 'outcome_unavailable' | 'outcome_deadline_reached';
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

