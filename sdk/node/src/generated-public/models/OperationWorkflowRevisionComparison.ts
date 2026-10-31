/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowChangeReason } from './OperationWorkflowChangeReason.js';
import type { OperationWorkflowConditionComparison } from './OperationWorkflowConditionComparison.js';
import type { OperationWorkflowRevisionChange } from './OperationWorkflowRevisionChange.js';
import type { OperationWorkflowRevisionEvidence } from './OperationWorkflowRevisionEvidence.js';
/**
 * Derived comparison with the preceding retained report on this history page in the same workflow run and tenant. Gaps and unavailable baselines prevent inferred changes or causal claims.
 */
export type OperationWorkflowRevisionComparison = {
  condition_comparison?: OperationWorkflowConditionComparison;
  change_reason?: OperationWorkflowChangeReason;
  status: 'compared' | 'baseline_unavailable' | 'revision_gap' | 'contract_changed' | 'ambiguous_revision';
  summary: string;
  previous_report_id?: string;
  previous_operation_id?: string;
  previous_revision?: number;
  changes: Array<OperationWorkflowRevisionChange>;
  evidence: Array<OperationWorkflowRevisionEvidence>;
};

