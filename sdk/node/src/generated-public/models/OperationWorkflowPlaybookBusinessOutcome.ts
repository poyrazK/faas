/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookOutcomeAcceptancePolicy } from './OperationWorkflowPlaybookOutcomeAcceptancePolicy.js';
import type { OperationWorkflowPlaybookOutcomeGuidance } from './OperationWorkflowPlaybookOutcomeGuidance.js';
import type { OperationWorkflowReadinessBusinessOutcome } from './OperationWorkflowReadinessBusinessOutcome.js';
/**
 * OperationWorkflowPlaybookBusinessOutcome. Exact-case application business result compared with a frozen playbook expectation. Independent of human completion and evidence satisfaction; changed guidance never silently changes the expected outcome.
 */
export type OperationWorkflowPlaybookBusinessOutcome = {
  /**
   * OperationWorkflowPlaybookBusinessOutcome.outcome_guidance. Frozen complete guidance set retained for later exact-contract observations; never an execution instruction.
   */
  outcome_guidance?: Array<OperationWorkflowPlaybookOutcomeGuidance>;
  /**
   * OperationWorkflowPlaybookBusinessOutcome.guidance. Matching pinned guidance for the fresh comparable reported terminal outcome; omitted for pending or unavailable reports.
   */
  guidance?: OperationWorkflowPlaybookOutcomeGuidance;
  acceptance_policy?: OperationWorkflowPlaybookOutcomeAcceptancePolicy;
  /**
   * OperationWorkflowPlaybookBusinessOutcome.acceptance. Classification under the frozen explicit policy. Omitted for legacy playbooks; alternatives never change the primary expected-outcome status.
   */
  acceptance?: 'expected' | 'acceptable_alternative' | 'unexpected' | 'pending' | 'unavailable';
  /**
   * OperationWorkflowPlaybookBusinessOutcome.deadline_at. Pinned expected-result deadline, if configured.
   */
  deadline_at?: string;
  /**
   * OperationWorkflowPlaybookBusinessOutcome.deadline_reached. The observation clock is at or past the deadline; does not establish failure or historical timeliness.
   */
  deadline_reached?: boolean;
  /**
   * OperationWorkflowPlaybookBusinessOutcome.status. Only a fresh comparable explicit terminal outcome establishes achieved or different outcome. Missing state, stale reports, missing terminal outcome and contract changes are unavailable.
   */
  status: 'pending' | 'achieved' | 'different_outcome' | 'unavailable';
  /**
   * OperationWorkflowPlaybookBusinessOutcome.reason. Stable comparison reason; changing clocks and report revisions alone does not change the business result.
   */
  reason: string;
  /**
   * OperationWorkflowPlaybookBusinessOutcome.evidence. Shared application-outcome observation with report identity and clock. Under unavailable, the inner evaluator status is unknown and any reported code is diagnostic, not a comparable business result.
   */
  evidence: OperationWorkflowReadinessBusinessOutcome;
};

