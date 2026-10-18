/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Repair plan metadata in Operation history; captured row values remain in the application executor.
 */
export type OperationDataRepair = {
  source_operation_id: string;
  plan_id: string;
  plan_digest: string;
  phase: 'prepared' | 'rehearsing' | 'awaiting_approval' | 'approved' | 'applying' | 'outcome_unknown' | 'succeeded' | 'failed' | 'rejected' | 'replacing' | 'replaced' | 'resolving' | 'closed';
  /**
   * Omitted on legacy records; health observations conservatively use creation time.
   */
  phase_started_at?: string;
  candidates: number;
  conflicts: number;
  unavailable: number;
  plan_expires_at: string;
  approval?: {
    step: string;
    decision: 'approved' | 'rejected';
    actor: {
      kind: 'account';
      id: string;
    };
    comment?: string;
  };
  applied_at?: string;
  rehearsal_task_id?: string;
  rehearsal_status?: 'queued' | 'running' | 'cleanup' | 'publishing' | 'succeeded' | 'failed' | 'cancelled';
  rehearsal_cancel_requested?: boolean;
  /**
   * Original parent plan for a linked replacement; omitted on root plans.
   */
  replaces_plan_id?: string;
  /**
   * Exact frozen parent digest; approval authority is never inherited.
   */
  replaces_plan_digest?: string;
  /**
   * Source-confirmed permanent closure without an apply commit. Replacement requires fresh rehearsal and approval.
   */
  apply_closed_at?: string;
  /**
   * Reserved replacement UUID. Resume the same identity after response loss.
   */
  replacement_operation_id?: string;
};

