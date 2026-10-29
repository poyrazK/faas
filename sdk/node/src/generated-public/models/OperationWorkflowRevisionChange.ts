/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Deterministic reported state or blocker or path or decision change. Before and after contain bounded public snapshot text; removal of a blocker does not prove business resolution.
 */
export type OperationWorkflowRevisionChange = {
  kind: 'state_changed' | 'blocker_added' | 'blocker_removed' | 'blocker_updated' | 'path_selected' | 'path_changed' | 'decision_changed' | 'contract_changed';
  key: string;
  before?: string;
  after?: string;
  explanation: string;
};

