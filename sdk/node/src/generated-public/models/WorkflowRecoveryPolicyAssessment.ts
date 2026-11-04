/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Eligibility from preserved facts, conditional preparation result and lower-bound pressure from observed applications.
 */
export type WorkflowRecoveryPolicyAssessment = {
  eligibility: 'qualifies' | 'excluded' | 'unknown';
  reason: string;
  preparation: 'would_prepare' | 'excluded' | 'unknown' | 'cooldown' | 'hourly_budget' | 'already_prepared';
  execution_pressure: 'not_evaluated' | 'not_enabled' | 'observed_hourly_budget' | 'observed_cooldown' | 'insufficient_history';
};

