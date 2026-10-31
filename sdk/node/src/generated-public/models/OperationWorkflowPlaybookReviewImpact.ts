/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowPlaybookReviewImpact. Frozen application guidance explaining the business result protected by human review; advisory confidence, independent of reported business state.
 */
export type OperationWorkflowPlaybookReviewImpact = {
  /**
   * OperationWorkflowPlaybookReviewImpact.kind. Result category in this workflow case.
   */
  kind: 'milestone' | 'outcome';
  /**
   * OperationWorkflowPlaybookReviewImpact.code. Milestone slug or expected or explicitly acceptable outcome code from this pinned playbook. A milestone name is declared guidance, not verified existence or completion.
   */
  code: string;
  /**
   * OperationWorkflowPlaybookReviewImpact.description. Application-authored explanation of the business risk; trimmed control-free UTF-8 text limited to 1024 bytes.
   */
  description: string;
  /**
   * OperationWorkflowPlaybookReviewImpact.priority. Declared priority for operator triage, independent of deadline and observed business success.
   */
  priority: 'normal' | 'high' | 'critical';
};

