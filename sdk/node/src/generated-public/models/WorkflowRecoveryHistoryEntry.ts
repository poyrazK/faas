/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryCandidateEvidence } from './WorkflowRecoveryCandidateEvidence.js';
import type { WorkflowRecoveryPolicy } from './WorkflowRecoveryPolicy.js';
import type { WorkflowRecoveryPollingNotification } from './WorkflowRecoveryPollingNotification.js';
/**
 * A retained event or latest observation linked to its available recovery evidence.
 */
export type WorkflowRecoveryHistoryEntry = {
  polling_notification?: WorkflowRecoveryPollingNotification;
  initial_failure_id?: string;
  evidence_kind?: 'parent' | 'child' | 'initial';
  resume_count?: number;
  request_id?: string;
  notification_event_id?: string;
  diagnosis_reason?: string;
  review_url?: string;
  evidence_url?: string;
  job_url?: string;
  candidate_evidence?: WorkflowRecoveryCandidateEvidence;
  /**
   * True for an immutable event recorded prospectively with a state mutation; absent for retained source observations.
   */
  durable?: boolean;
  /**
   * Worker transition classification; suppression reason remains in reason.
   */
  evaluation_reason?: string;
  /**
   * Health incident episode association preserved across later incidents.
   */
  episode_id?: string;
  /**
   * Frozen configuration on policy_changed events; excludes workflow execution inputs and outputs.
   */
  policy?: WorkflowRecoveryPolicy;
  id: string;
  occurred_at: string;
  kind: string;
  outcome: string;
  reason: string;
  policy_version?: number;
  workflow?: string;
  run_id?: string;
  review_id?: string;
  action_id?: string;
  job_id?: string;
  child_job_id?: string;
  actor_kind?: string;
  actor_id?: string;
};

