/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowCaseAction } from './OperationWorkflowCaseAction.js';
import type { OperationWorkflowCaseNextAction } from './OperationWorkflowCaseNextAction.js';
import type { OperationWorkflowCaseObligationHistoryItem } from './OperationWorkflowCaseObligationHistoryItem.js';
import type { OperationWorkflowCoordinationTimeline } from './OperationWorkflowCoordinationTimeline.js';
import type { OperationWorkflowInstanceSnapshot } from './OperationWorkflowInstanceSnapshot.js';
import type { OperationWorkflowObligationAssessment } from './OperationWorkflowObligationAssessment.js';
import type { OperationWorkflowObligationContext } from './OperationWorkflowObligationContext.js';
import type { OperationWorkflowPlaybookGuidance } from './OperationWorkflowPlaybookGuidance.js';
import type { OperationWorkflowPlaybookProgress } from './OperationWorkflowPlaybookProgress.js';
import type { OperationWorkflowReadinessFollowUp } from './OperationWorkflowReadinessFollowUp.js';
/**
 * OperationWorkflowCase. Current evidence and advisory coordination for an exact scoped business workflow.
 */
export type OperationWorkflowCase = {
  obligation_context?: OperationWorkflowObligationContext;
  obligation_assessments: Array<OperationWorkflowObligationAssessment>;
  obligation_history: Array<OperationWorkflowCaseObligationHistoryItem>;
  /**
   * Matching obligation groups found in this bounded retained source scan.
   */
  obligation_history_count: number;
  /**
   * Known receipt pruning or response caps limited this history view.
   */
  obligation_history_truncated: boolean;
  /**
   * Deleted sources and older receipts without case attribution are unavailable; missing events never establish completion.
   */
  obligation_history_limited: boolean;
  obligation_history_checked_at: string;
  /**
   * Current obligations and suggestions from retained source records, matched to the exact case before follow-up page truncation. Ordered overdue first, then dated work by due time. Historical delivery snapshots do not create work.
   */
  next_actions: Array<OperationWorkflowCaseNextAction>;
  /**
   * Number of retained matching action rows before the 100-row cap. One source can have several obligations; this is not a workflow or global task count.
   */
  next_action_count: number;
  /**
   * Additional retained actions were omitted from the bounded response.
   */
  next_actions_truncated: boolean;
  /**
   * OperationWorkflowCase.playbook_progress. Durable pinned checklists for this exact case, retained independently of inboxes and current guidance; at most 100.
   */
  playbook_progress: Array<OperationWorkflowPlaybookProgress>;
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

