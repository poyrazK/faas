/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Historical automation.recovery_queue_blocked event. Saved-failure retries capture changes in exact parent or child evidence diagnosis and proven clearance, with diagnosis URL and CLI guidance. Initial automatic recovery identifies initial_failure_id. Unchanged blockers stay quiet; recovery identifies cleared evidence rather than successful execution. Capturing this event does not finalize a child, release its execution slot, or authorize another retry. Delivery may occur after the block resolves; stable identity and frozen recipients support receiver deduplication.
 */
export type WorkflowRecoveryQueueBlockedWebhookPayload = {
  evidence_kind?: 'parent' | 'child' | 'initial';
  diagnosis_reason?: string;
  evidence_url?: string;
  run_id?: string;
  resume_count?: number;
  repairable?: boolean;
  cli_command?: string;
  initial_failure_id?: string;
  state?: 'blocked' | 'recovered';
  previous_reason?: string;
  operator_action?: string;
  job_url?: string;
  event_id: string;
  app_id: string;
  app: string;
  review_id: string;
  action_id: string;
  policy_version: number;
  position: number;
  child_job_id?: string;
  status: 'pending' | 'applied';
  reason: 'evidence_unknown' | 'terminal_outcome_not_saved' | 'execution_quota' | 'job_paused' | 'blocker_cleared';
  observed_at: string;
  queue_url: string;
  review_status_url: string;
};

