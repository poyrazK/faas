/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryInitialPolicyChangePreview } from './WorkflowRecoveryInitialPolicyChangePreview.js';
import type { WorkflowRecoveryPolicy } from './WorkflowRecoveryPolicy.js';
import type { WorkflowRecoveryPolicyChangeCandidate } from './WorkflowRecoveryPolicyChangeCandidate.js';
import type { WorkflowRecoveryPolicyChangeEntry } from './WorkflowRecoveryPolicyChangeEntry.js';
import type { WorkflowRecoveryPolicyResponse } from './WorkflowRecoveryPolicyResponse.js';
/**
 * Consistent read-only snapshot of a proposed full replacement. Budget and cooldown observations retain spent charges and preparation targets across versions. Replacement clears automatic pause and failure streak; current contains that historical state. Remaining budgets describe proposed settings even when disabled. Serial slots, inspection errors and execution quota can still prevent progress. No update is authorized by this response; apply the same expected version with deploy write access and MFA. Queue and evidence may change without changing the configuration version.
 */
export type WorkflowRecoveryPolicyChangePreview = {
  initial_failures?: WorkflowRecoveryInitialPolicyChangePreview;
  current: WorkflowRecoveryPolicyResponse;
  proposed: WorkflowRecoveryPolicy;
  observed_at: string;
  next_version: number;
  pending_suppressed: number;
  outstanding_retained: number;
  entries: Array<WorkflowRecoveryPolicyChangeEntry>;
  entries_truncated: boolean;
  automatic_execution_enabled: boolean;
  hourly_executions: number;
  remaining_execution_budget: number;
  execution_cooldown_until?: string;
  hourly_prepared_actions: number;
  remaining_preparation_budget: number;
  preparation_cooldown_until?: string;
  inspected_jobs: number;
  jobs_truncated: boolean;
  candidates: Array<WorkflowRecoveryPolicyChangeCandidate>;
  candidates_truncated: boolean;
};

