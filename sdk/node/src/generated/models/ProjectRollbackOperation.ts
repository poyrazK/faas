/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectRollbackAutoUndo } from './ProjectRollbackAutoUndo.js';
import type { ProjectRollbackStep } from './ProjectRollbackStep.js';
/**
 * A rollback plan or durable receipt with per-service cutovers and verification.
 */
export type ProjectRollbackOperation = {
  operation_id?: string;
  project: string;
  project_id: string;
  /**
   * The request ID saved when this rollback was accepted.
   */
  request_id?: string;
  /**
   * Hash identifying the runnable plan returned by preview or saved at admission.
   */
  plan_hash?: string;
  /**
   * Hash from a runnable resume preview or the latest accepted reviewed resume.
   */
  resume_plan_hash?: string;
  /**
   * Source rollback whose cutovers this receipt undoes.
   */
  undo_of?: string;
  /**
   * Accepted undo operation linked to this paused rollback.
   */
  undo_operation_id?: string;
  deploy_group_id?: string;
  acknowledge_releases?: boolean;
  /**
   * Whether configured HTTP health verification is required.
   */
  verify?: boolean;
  /**
   * Whether configured probes must pass before project traffic moves.
   */
  verify_before_cutover?: boolean;
  /**
   * Saved duration of the post-cutover health stabilization window.
   */
  stabilize_for_seconds?: number;
  /**
   * Whether configured production 5xx budgets gate the rollback.
   */
  verify_live_traffic?: boolean;
  /**
   * Whether a confirmed health probe 5xx can trigger automatic undo.
   */
  undo_on_health_failure?: boolean;
  /**
   * Whether a sustained production 5xx breach can trigger automatic undo.
   */
  undo_on_live_traffic_failure?: boolean;
  auto_undo?: ProjectRollbackAutoUndo;
  status: 'planned' | 'running' | 'blocked' | 'complete';
  /**
   * Preparation phase of the operation, either preparing, cutover, or releasing.
   */
  phase?: string;
  /**
   * When an operator requested the current pause. Cleared on resume.
   */
  pause_requested_at?: string;
  /**
   * Deadline for preparation
   */
  preparation_deadline?: string;
  steps: Array<ProjectRollbackStep>;
  created_at?: string;
  updated_at?: string;
  /**
   * Time from project rollback acceptance until the last targeted HTTP service was verified.
   */
  recovery_ms?: number;
};

