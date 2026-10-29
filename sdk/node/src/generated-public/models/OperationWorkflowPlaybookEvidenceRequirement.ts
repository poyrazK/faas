/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowPlaybookEvidenceRequirement. One pinned live application-evidence selector per checklist label. Milestone selects a declared operation and milestone. Prerequisite additionally selects an exact current-state transition and producer, optionally an alternative group and named path. Verification selects current-report blocker resolutions; absent obligations are unavailable.
 */
export type OperationWorkflowPlaybookEvidenceRequirement = {
  /**
   * OperationWorkflowPlaybookEvidenceRequirement.label. Exact label from required_evidence; unique within evidence_requirements.
   */
  label: string;
  /**
   * OperationWorkflowPlaybookEvidenceRequirement.kind. Selector category; invalid combinations of fields are rejected.
   */
  kind: 'milestone' | 'prerequisite' | 'verification';
  /**
   * OperationWorkflowPlaybookEvidenceRequirement.operation. Declared milestone producer, prerequisite transition operation, or blocker-resolution operation name.
   */
  operation: string;
  /**
   * OperationWorkflowPlaybookEvidenceRequirement.milestone. Required milestone name for milestone and prerequisite kinds; omitted for verification.
   */
  milestone?: string;
  /**
   * OperationWorkflowPlaybookEvidenceRequirement.prerequisite_operation. Required fact producer name for prerequisite kind only.
   */
  prerequisite_operation?: string;
  /**
   * OperationWorkflowPlaybookEvidenceRequirement.from_state. Required exact transition source for prerequisite kind only.
   */
  from_state?: string;
  /**
   * OperationWorkflowPlaybookEvidenceRequirement.to_state. Required exact transition destination for prerequisite kind only.
   */
  to_state?: string;
  /**
   * OperationWorkflowPlaybookEvidenceRequirement.group_id. Optional prerequisite alternative group. Without a group, selects mandatory retained facts.
   */
  group_id?: string;
  /**
   * OperationWorkflowPlaybookEvidenceRequirement.path_id. Optional named prerequisite path; requires group_id. Evaluates the selected fact, not the whole path or its decision.
   */
  path_id?: string;
  /**
   * OperationWorkflowPlaybookEvidenceRequirement.blocker_code. Required blocker code for verification kind only; all matching obligations on the current report must be verified.
   */
  blocker_code?: string;
};

