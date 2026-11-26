/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Progress and receipt IDs for a recorded project rollback or undo.
 */
export type ProjectRollbackSummary = {
  operation_id: string;
  /**
   * Current receipt status, either running, blocked, or complete.
   */
  status: string;
  phase?: string;
  cutovers_completed: number;
  /**
   * Number of planned service cutovers excluding skipped services.
   */
  cutovers_total: number;
  undo_of?: string;
  undo_operation_id?: string;
  pause_requested_at?: string;
  created_at?: string;
  updated_at?: string;
};

