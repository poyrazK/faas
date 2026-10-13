/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowConditionCoverageEntry } from './OperationWorkflowConditionCoverageEntry.js';
/**
 * Workflow-wide current-state expected condition coverage evaluated from the latest reporting Operation pinned definition and retained explicit decision contexts. It does not add transition guards. Counts precede previews. Unknown coverage is never inferred as satisfied; source evidence uses its own pinned freshness budget. Conditions apply to current state rather than proposed destination state.
 */
export type OperationWorkflowTransitionReporting = {
  evaluated_at: string;
  state: string;
  reporting_operation_id: string;
  status: 'compared' | 'unconfigured' | 'terminal' | 'definition_unavailable' | 'incomplete' | 'limit' | 'ambiguous';
  expected_condition_count: number;
  reported_condition_count: number;
  missing_condition_count: number;
  overdue_condition_count: number;
  conditions: Array<OperationWorkflowConditionCoverageEntry>;
  conditions_truncated: boolean;
};

