/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Last committed worker evaluation, never refreshed by reads or notification capture. Waiting_since is the first observation of this consecutive wait reason and resets when the reason changes. Absent for legacy or unevaluated intents. Pausing preserves historical evidence.
 */
export type WorkflowRecoveryAutomaticEvaluation = {
  evaluated_at: string;
  reason: 'hourly_budget' | 'cooldown' | 'execution_quota' | 'evidence_unknown' | 'outstanding_retry' | 'admission_wait' | 'execution_wait' | 'retry_running' | 'inspection_error' | 'child_created' | 'result_recorded' | 'suppressed' | 'hourly_execution_budget' | 'execution_cooldown' | 'outstanding_recovery' | 'recovery_running' | 'terminal_outcome_not_saved' | 'job_paused' | 'automatic_paused' | 'expired' | 'policy_changed' | 'policy_disabled' | 'changed';
  waiting_since?: string;
};

