/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowPlaybook. Application-owned advisory guidance; evidence requirements are human checklists and links are never executed by Gregale.
 */
export type OperationWorkflowPlaybook = {
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
   * OperationWorkflowPlaybook.required_evidence. Advisory evidence checklist, not automatically verified prerequisites.
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

