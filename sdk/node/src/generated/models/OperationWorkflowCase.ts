/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowCaseAction } from './OperationWorkflowCaseAction.js';
import type { OperationWorkflowCoordinationTimeline } from './OperationWorkflowCoordinationTimeline.js';
import type { OperationWorkflowInstanceSnapshot } from './OperationWorkflowInstanceSnapshot.js';
import type { OperationWorkflowPlaybookGuidance } from './OperationWorkflowPlaybookGuidance.js';
import type { OperationWorkflowReadinessFollowUp } from './OperationWorkflowReadinessFollowUp.js';
/**
 * OperationWorkflowCase. Current evidence and advisory coordination for an exact scoped business workflow.
 */
export type OperationWorkflowCase = {
  /**
   * OperationWorkflowCase.guidance. Matching application playbooks from the definition that reported current fresh nonterminal evidence.
   */
  guidance: Array<OperationWorkflowPlaybookGuidance>;
  /**
   * OperationWorkflowCase.guidance_status. Availability of exact reporting-definition playbooks, independent of matches and readiness.
   */
  guidance_status: 'available' | 'not_declared' | 'evidence_unavailable' | 'definition_unavailable';
  /**
   * OperationWorkflowCase.guidance_truncated. Matching guidance or the evaluated transition evidence was truncated.
   */
  guidance_truncated: boolean;
  /**
   * OperationWorkflowCase.status. Unknown, stale, terminal_reported, no_transition, blocked or ready. Terminal state alone is not success.
   */
  status: string;
  /**
   * OperationWorkflowCase.summary. Current evidence interpretation with application authority boundaries.
   */
  summary: string;
  /**
   * OperationWorkflowCase.instance. Current retained exact-customer workflow projection; omitted when no contract projection exists.
   */
  instance?: OperationWorkflowInstanceSnapshot;
  /**
   * OperationWorkflowCase.follow_ups. Matching open scheduled follow-ups, ordered by due clock; independent of application readiness.
   */
  follow_ups: Array<OperationWorkflowReadinessFollowUp>;
  /**
   * OperationWorkflowCase.follow_up_count. Total matching retained open scheduled follow-ups before overview truncation.
   */
  follow_up_count: number;
  /**
   * OperationWorkflowCase.overdue_follow_up_count. Matching retained follow-ups due by coordination check time.
   */
  overdue_follow_up_count: number;
  /**
   * OperationWorkflowCase.follow_ups_truncated. Only the first 100 matching follow-ups are included.
   */
  follow_ups_truncated: boolean;
  /**
   * OperationWorkflowCase.actions. At most 100 evidence-based suggestions, with stale/missing evidence requiring a refresh.
   */
  actions: Array<OperationWorkflowCaseAction>;
  /**
   * OperationWorkflowCase.actions_truncated. Additional suggestions exceeded the bounded overview.
   */
  actions_truncated: boolean;
  /**
   * OperationWorkflowCase.timeline. Exact scope and identity with the independently read retained coordination event page.
   */
  timeline: OperationWorkflowCoordinationTimeline;
  /**
   * OperationWorkflowCase.evidence_checked_at. Server observation clock after the application projection read.
   */
  evidence_checked_at: string;
  /**
   * OperationWorkflowCase.coordination_checked_at. Server clock used to classify follow-up overdue status.
   */
  coordination_checked_at: string;
  /**
   * OperationWorkflowCase.evaluated_at. Server completion clock for the independent live reads.
   */
  evaluated_at: string;
  /**
   * OperationWorkflowCase.limitations. Explicit read consistency, retention and application authority boundaries.
   */
  limitations: Array<string>;
};

