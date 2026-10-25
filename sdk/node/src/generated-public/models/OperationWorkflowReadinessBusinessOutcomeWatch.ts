/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessBusinessOutcome } from './OperationWorkflowReadinessBusinessOutcome.js';
import type { OperationWorkflowReadinessOutcomeEscalationPolicy } from './OperationWorkflowReadinessOutcomeEscalationPolicy.js';
import type { OperationWorkflowReadinessOutcomeEscalationReference } from './OperationWorkflowReadinessOutcomeEscalationReference.js';
/**
 * Retained opt-in watch for one source completion business criterion. Current observations remain advisory; deadlines are notified once and unchanged status is suppressed. Source inbox retention and subscription deletion end monitoring.
 */
export type OperationWorkflowReadinessBusinessOutcomeWatch = {
  escalation?: OperationWorkflowReadinessOutcomeEscalationReference;
  escalation_policy?: OperationWorkflowReadinessOutcomeEscalationPolicy;
  /**
   * OperationWorkflowReadinessBusinessOutcomeWatch.id. Retained advisory business outcome watch metadata; does not modify application reports or coordination revisions.
   */
  id: string;
  /**
   * OperationWorkflowReadinessBusinessOutcomeWatch.revision. Retained advisory business outcome watch metadata; does not modify application reports or coordination revisions.
   */
  revision: number;
  /**
   * OperationWorkflowReadinessBusinessOutcomeWatch.state_revision. Retained advisory business outcome watch metadata; does not modify application reports or coordination revisions.
   */
  state_revision: number;
  /**
   * OperationWorkflowReadinessBusinessOutcomeWatch.started_at. Retained advisory business outcome watch metadata; does not modify application reports or coordination revisions.
   */
  started_at: string;
  /**
   * OperationWorkflowReadinessBusinessOutcomeWatch.deadline_at. Retained advisory business outcome watch metadata; does not modify application reports or coordination revisions.
   */
  deadline_at?: string;
  /**
   * OperationWorkflowReadinessBusinessOutcomeWatch.deadline_notified. Retained advisory business outcome watch metadata; does not modify application reports or coordination revisions.
   */
  deadline_notified: boolean;
  /**
   * OperationWorkflowReadinessBusinessOutcomeWatch.last_notified_status. Retained advisory business outcome watch metadata; does not modify application reports or coordination revisions.
   */
  last_notified_status?: 'achieved' | 'pending' | 'different_outcome' | 'unknown';
  current: OperationWorkflowReadinessBusinessOutcome;
};

