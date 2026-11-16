/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TaskToolRecoveryQualification } from './TaskToolRecoveryQualification.js';
import type { TaskToolRetirementResult } from './TaskToolRetirementResult.js';
/**
 * Separate fixed owner approval for atomic native conditional parking. Pending means a park request committed; retired also requires native drain and absent or expired Task registrations. Restore and quarantine approvals do not authorize retirement.
 */
export type TaskToolRetirement = {
  qualification: TaskToolRecoveryQualification;
  state: 'qualified' | 'retirement_pending' | 'retired';
  decision?: 'approved' | 'revoked';
  authorizer_key_id?: string;
  decided_at?: string;
  park_transition_id?: string;
  park_requested_at?: string;
  result?: TaskToolRetirementResult;
};

