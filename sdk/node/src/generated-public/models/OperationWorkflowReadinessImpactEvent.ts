/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessEscalationResponse } from './OperationWorkflowReadinessEscalationResponse.js';
import type { OperationWorkflowReadinessImpactSummary } from './OperationWorkflowReadinessImpactSummary.js';
/**
 * OperationWorkflowReadinessImpactEvent. Protected business result monitoring of retained unresolved human reviews in a scoped application environment; advisory confidence only.
 */
export type OperationWorkflowReadinessImpactEvent = {
  /**
   * OperationWorkflowReadinessImpactEvent.response. Immutable alert acknowledgment target and retained assignment history; source review clearance does not acknowledge the alert.
   */
  response?: OperationWorkflowReadinessEscalationResponse;
  /**
   * OperationWorkflowReadinessImpactEvent.workflow. Exact workflow identity.
   */
  workflow: string;
  /**
   * OperationWorkflowReadinessImpactEvent.kind. Protected result category.
   */
  kind: 'milestone' | 'outcome';
  /**
   * OperationWorkflowReadinessImpactEvent.code. Exact protected result slug.
   */
  code: string;
  /**
   * OperationWorkflowReadinessImpactEvent.checked_at. Advisory sample time.
   */
  checked_at: string;
  /**
   * OperationWorkflowReadinessImpactEvent.reasons. First critical or overdue review transition, or source-confirmed clearance of tracked obligations; stable observations remain silent.
   */
  reasons: Array<'critical_review_added' | 'review_became_overdue' | 'reviews_cleared'>;
  /**
   * OperationWorkflowReadinessImpactEvent.previous. Previous accepted baseline, absent for zero reviews.
   */
  previous?: OperationWorkflowReadinessImpactSummary;
  /**
   * OperationWorkflowReadinessImpactEvent.current. Latest accepted sample, absent for source-confirmed zero reviews.
   */
  current?: OperationWorkflowReadinessImpactSummary;
};

