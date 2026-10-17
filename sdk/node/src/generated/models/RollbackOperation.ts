/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingCheckFinding } from './BindingCheckFinding.js';
/**
 * Durable progress of a rollback. A checked historical rollback reports its binding gates; a plain rollback (manual, dashboard, alert, or first-wake 5xx auto-rollback, ADR-1446) reports preparing until its target serves (complete, with recovery_ms) or fails. This receipt grants no authority to reuse binding evidence or move traffic for another operation.
 */
export type RollbackOperation = {
  id: string;
  app_id: string;
  scope: string;
  target_deployment_id: string;
  current_deployment_id: string;
  status: 'preparing' | 'ready' | 'blocked' | 'routing' | 'complete' | 'failed';
  service: boolean;
  reason?: string;
  code?: string;
  blockers?: Array<BindingCheckFinding>;
  created_at: string;
  updated_at: string;
  completed_at?: string | null;
  audit_id?: string;
  /**
   * What started a plain rollback. Absent for a checked rollback.
   */
  trigger?: 'manual' | 'auto_5xx' | 'alert';
  /**
   * Milliseconds from the plain rollback request until its target served. Present once a plain rollback is complete.
   */
  recovery_ms?: number;
  /**
   * Why a failed plain rollback did not restore the previous release; code carries the stable error code.
   */
  error?: string;
};

