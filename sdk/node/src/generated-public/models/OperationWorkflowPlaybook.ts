/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookEvidenceRequirement } from './OperationWorkflowPlaybookEvidenceRequirement.js';
import type { OperationWorkflowPlaybookOutcomeAcceptancePolicy } from './OperationWorkflowPlaybookOutcomeAcceptancePolicy.js';
import type { OperationWorkflowPlaybookOutcomeDeadlinePolicy } from './OperationWorkflowPlaybookOutcomeDeadlinePolicy.js';
import type { OperationWorkflowPlaybookOutcomeGuidance } from './OperationWorkflowPlaybookOutcomeGuidance.js';
/**
 * OperationWorkflowPlaybook. Application-owned advisory guidance; evidence requirements are human checklists and links are never executed by Gregale.
 */
export type OperationWorkflowPlaybook = {
  /**
   * OperationWorkflowPlaybook.outcome_guidance. Optional pinned descriptions and suggested follow-up for the expected outcome and explicitly allowed alternatives; unique by outcome code.
   */
  outcome_guidance?: Array<OperationWorkflowPlaybookOutcomeGuidance>;
  outcome_acceptance?: OperationWorkflowPlaybookOutcomeAcceptancePolicy;
  /**
   * OperationWorkflowPlaybook.outcome_deadline. Optional application attention policy requiring expected_outcome_code. Frozen with guidance; deadline starts at checklist creation, not watch enrollment.
   */
  outcome_deadline?: OperationWorkflowPlaybookOutcomeDeadlinePolicy;
  /**
   * OperationWorkflowPlaybook.expected_outcome_code. Optional expected application terminal outcome, frozen with guidance; does not infer causation or close coordination.
   */
  expected_outcome_code?: string;
  /**
   * OperationWorkflowPlaybook.id. Unique bounded lowercase identifier within the workflow contract.
   */
  id: string;
  /**
   * OperationWorkflowPlaybook.version. Explicit playbook revision, pinned alongside the workflow contract version.
   */
  version: number;
  /**
   * OperationWorkflowPlaybook.match_kind. Exact observed fact category.
   */
  match_kind: 'blocker' | 'missing_milestone' | 'missing_dependency' | 'pending_verification' | 'requirement';
  /**
   * OperationWorkflowPlaybook.match_code. Exact blocker code, milestone, dependency workflow, verification blocker code or readiness reason code.
   */
  match_code: string;
  /**
   * OperationWorkflowPlaybook.summary. Application-authored guidance without control characters or surrounding whitespace.
   */
  summary: string;
  /**
   * OperationWorkflowPlaybook.owner. Suggested responsible team; does not assign or override coordination ownership.
   */
  owner?: string;
  /**
   * OperationWorkflowPlaybook.evidence_requirements. Optional unique live selectors for declared checklist labels. Frozen with guidance; human reviews remain independent.
   */
  evidence_requirements?: Array<OperationWorkflowPlaybookEvidenceRequirement>;
  /**
   * OperationWorkflowPlaybook.required_evidence. Human checklist labels with optional live application-evidence selectors.
   */
  required_evidence?: Array<string>;
  /**
   * OperationWorkflowPlaybook.runbook_url. Absolute HTTPS runbook link without user credentials; never fetched automatically.
   */
  runbook_url?: string;
  /**
   * OperationWorkflowPlaybook.action_url. Absolute HTTPS application action link without user credentials; application handles authorization and execution.
   */
  action_url?: string;
};

