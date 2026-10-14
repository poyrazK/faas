/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Latest blocked or recovered evidence capture for a retry or initial-recovery review. Retry sequence increases only on a changed evidence diagnosis or proven clearance. Receivers are frozen at capture; queued identifies ledger admission rather than successful delivery. No receivers prevents historical replay to later subscribers.
 */
export type WorkflowRecoveryAutomaticBlockedNotification = {
  state?: 'blocked' | 'recovered';
  sequence?: number;
  evidence_kind?: 'parent' | 'child' | 'initial';
  diagnosis_reason?: string;
  event_id: string;
  status: 'queued' | 'no_receivers';
  reason: 'evidence_unknown' | 'terminal_outcome_not_saved' | 'execution_quota' | 'job_paused' | 'blocker_cleared';
  captured_at: string;
  receiver_webhook_ids: Array<string>;
};

