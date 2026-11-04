/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Fresh exact-case transition context shared by obligation assessments. Detailed declared requirements and dependency identities remain in instance.readiness, avoiding duplicated evidence payloads. Stale or absent reports never produce a positive transition verdict; a satisfied edge does not grant application execution or bind a free-form plan.
 */
export type OperationWorkflowObligationContext = {
  status: 'evidence_unavailable' | 'terminal_reported' | 'transition_requirements_satisfied' | 'partial_readiness' | 'no_declared_transition' | 'waiting_on_requirements';
  summary: string;
  checked_at: string;
  coverage_incomplete: boolean;
  transition_count: number;
  evaluated_transition_count: number;
};

