/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowPlaybookOutcomeEscalation } from './OperationWorkflowPlaybookOutcomeEscalation.js';
import type { OperationWorkflowPlaybookStepAssignment } from './OperationWorkflowPlaybookStepAssignment.js';
import type { OperationWorkflowReadinessCoordination } from './OperationWorkflowReadinessCoordination.js';
import type { OperationWorkflowReadinessDueReminder } from './OperationWorkflowReadinessDueReminder.js';
import type { OperationWorkflowReadinessImpactEvent } from './OperationWorkflowReadinessImpactEvent.js';
import type { OperationWorkflowReadinessOutcomeEscalation } from './OperationWorkflowReadinessOutcomeEscalation.js';
import type { OperationWorkflowReviewResponse } from './OperationWorkflowReviewResponse.js';
/**
 * Compact pending follow-up with original inbox references; no application readiness is inferred.
 */
export type OperationWorkflowReadinessFollowUp = {
  /**
   * OperationWorkflowReadinessFollowUp.attention_due_at. Earlier of human check-in and active decision revalidation deadline, used for queue ordering and overdue filters; coordination remains unchanged.
   */
  attention_due_at?: string;
  /**
   * OperationWorkflowReadinessFollowUp.review_response. Explicit human response ownership, plan and check-in linked to protected-result reviews; separate from alert acknowledgment and application execution.
   */
  review_response?: OperationWorkflowReviewResponse;
  /**
   * OperationWorkflowReadinessFollowUp.business_impact. Aggregate alert context and its acknowledgment target; no individual subject or transition is inferred.
   */
  business_impact?: OperationWorkflowReadinessImpactEvent;
  playbook_step?: OperationWorkflowPlaybookStepAssignment;
  /**
   * OperationWorkflowReadinessFollowUp.playbook_escalation. Live evidence of an assigned playbook deadline follow-up, separate from human coordination and immutable delivery evidence.
   */
  playbook_escalation?: OperationWorkflowPlaybookOutcomeEscalation;
  outcome_escalation?: OperationWorkflowReadinessOutcomeEscalation;
  /**
   * OperationWorkflowReadinessFollowUp.subscription_id. Retained notification or subscription identity; tenant fields are account-only.
   */
  subscription_id: string;
  /**
   * OperationWorkflowReadinessFollowUp.subscription_name. Retained notification or subscription identity; tenant fields are account-only.
   */
  subscription_name: string;
  /**
   * OperationWorkflowReadinessFollowUp.subscription_tenant_id. Retained notification or subscription identity; tenant fields are account-only.
   */
  subscription_tenant_id?: string;
  /**
   * OperationWorkflowReadinessFollowUp.platform_tenant_id. Retained notification or subscription identity; tenant fields are account-only.
   */
  platform_tenant_id?: string;
  /**
   * OperationWorkflowReadinessFollowUp.scope. Retained notification or subscription identity; tenant fields are account-only.
   */
  scope: string;
  /**
   * OperationWorkflowReadinessFollowUp.notification_id. Retained notification or subscription identity; tenant fields are account-only.
   */
  notification_id: string;
  /**
   * OperationWorkflowReadinessFollowUp.kind. Retained notification or subscription identity; tenant fields are account-only.
   */
  kind: string;
  /**
   * OperationWorkflowReadinessFollowUp.workflow. Retained notification or subscription identity; tenant fields are account-only.
   */
  workflow?: string;
  /**
   * OperationWorkflowReadinessFollowUp.instance_id. Retained notification or subscription identity; tenant fields are account-only.
   */
  instance_id?: string;
  /**
   * OperationWorkflowReadinessFollowUp.operation. Retained notification or subscription identity; tenant fields are account-only.
   */
  operation?: string;
  /**
   * OperationWorkflowReadinessFollowUp.from_state. Retained notification or subscription identity; tenant fields are account-only.
   */
  from_state?: string;
  /**
   * OperationWorkflowReadinessFollowUp.to_state. Retained notification or subscription identity; tenant fields are account-only.
   */
  to_state?: string;
  /**
   * OperationWorkflowReadinessFollowUp.sequence. Subscription-local notification sequence used for edits and evidence checks.
   */
  sequence: number;
  /**
   * OperationWorkflowReadinessFollowUp.created_at. Original notification creation time.
   */
  created_at: string;
  coordination: OperationWorkflowReadinessCoordination;
  subject?: OperationSubject;
  /**
   * OperationWorkflowReadinessFollowUp.overdue. Follow-up time is at or before this response evaluation clock.
   */
  overdue: boolean;
  reminder?: OperationWorkflowReadinessDueReminder;
};

