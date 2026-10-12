/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Last committed worker evaluation, never refreshed by reads or notification capture. Waiting_since is the first observation of this consecutive wait reason and resets when the reason changes. Absent for legacy or unevaluated intents. Pausing preserves historical evidence.
 */
export type WorkflowRecoveryAutomaticEvaluation = {
  evaluated_at: string;
  reason: 'hourly_budget' | 'cooldown' | 'execution_quota' | 'evidence_unknown' | 'outstanding_retry' | 'admission_wait' | 'execution_wait' | 'retry_running' | 'inspection_error' | 'child_created' | 'result_recorded' | 'suppressed';
  waiting_since?: string;
};

