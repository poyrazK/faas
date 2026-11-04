/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookStepAssignmentPolicy } from './OperationWorkflowPlaybookStepAssignmentPolicy.js';
import type { OperationWorkflowPlaybookStepDependency } from './OperationWorkflowPlaybookStepDependency.js';
/**
 * OperationWorkflowPlaybookOutcomeGuidance. Application-defined business meaning and optional suggested human follow-up, frozen with the playbook version. Plain text only; never establishes execution authority or changes outcome acceptance.
 */
export type OperationWorkflowPlaybookOutcomeGuidance = {
  /**
   * OperationWorkflowPlaybookOutcomeGuidance.follow_up_dependencies. Optional pinned acyclic prerequisite graph within this outcome's follow-up steps.
   */
  follow_up_dependencies?: Array<OperationWorkflowPlaybookStepDependency>;
  /**
   * OperationWorkflowPlaybookOutcomeGuidance.follow_up_assignments. Optional team and completion window for declared steps; at most 16 assignments across the entire playbook.
   */
  follow_up_assignments?: Array<OperationWorkflowPlaybookStepAssignmentPolicy>;
  /**
   * OperationWorkflowPlaybookOutcomeGuidance.follow_up_steps. Optional ordered human follow-up labels, unique and at most 256 bytes each; tracked separately from original playbook evidence items.
   */
  follow_up_steps?: Array<string>;
  /**
   * OperationWorkflowPlaybookOutcomeGuidance.outcome_code. Primary expected outcome or explicit acceptable alternative; unique within the playbook guidance set.
   */
  outcome_code: string;
  /**
   * OperationWorkflowPlaybookOutcomeGuidance.description. Required trimmed UTF-8 plain-text business meaning, at most 1024 bytes and no control characters.
   */
  description: string;
  /**
   * OperationWorkflowPlaybookOutcomeGuidance.suggested_follow_up. Optional trimmed UTF-8 plain-text suggestion, at most 1024 bytes and no control characters; application-owned decisions and execution remain separate.
   */
  suggested_follow_up?: string;
};

