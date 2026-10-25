/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDecisionOutcomeEvidence } from './OperationWorkflowDecisionOutcomeEvidence.js';
/**
 * Evidence and explanation for one ongoing workflow with overdue condition reports. Rankings use the same token-bound sample, complete-history eligibility and pinned definitions as active condition coverage. Owner lists and unassigned condition counts include all qualifying identities before the 16-fact preview cap. The oldest evidence has the earliest selected overdue report timestamp with canonical identity breaking ties, and is included independently of the preview. This is observational prioritization without business severity or execution authority.
 */
export type OperationWorkflowConditionRefreshPriority = {
  oldest_evidence: OperationWorkflowDecisionOutcomeEvidence;
  /**
   * Age of the oldest selected overdue report at evaluated_at. This is report age rather than seconds past its refresh deadline.
   */
  oldest_evidence_age_seconds: number;
  /**
   * Full observed workflow blocked duration through evaluated_at; not time attributed to these conditions.
   */
  blocked_seconds: number;
  /**
   * All distinct assigned refresh owners across qualifying overdue conditions, sorted lexically. Independent of owner-group display and evidence preview limits.
   */
  refresh_owners: Array<string>;
  /**
   * Number of qualifying overdue conditions without a pinned refresh owner; independent of preview limits.
   */
  unassigned_condition_count: number;
  /**
   * Explains the selected age or blocked-duration ranking using observed evidence without assigning causation or severity.
   */
  explanation: string;
};

