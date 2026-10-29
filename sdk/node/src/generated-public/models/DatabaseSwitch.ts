/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DatabaseSwitchDrain } from './DatabaseSwitchDrain.js';
/**
 * A rollback that restores the app's managed PostgreSQL database with the code. The database is restored to restore_point as a new database, and the app moves to it and to the target release while it is fenced.
 */
export type DatabaseSwitch = {
  id: string;
  app_id: string;
  /**
   * The deployment scope the rollback restores.
   */
  scope: string;
  /**
   * From fencing until completed the app does not serve. aborting unwinds everything done before the bindings switched.
   */
  phase: 'restoring' | 'staging' | 'verifying' | 'fencing' | 'settling' | 'draining' | 'switching' | 'resuming' | 'aborting' | 'completed' | 'failed' | 'cancelled';
  /**
   * The release serving when the switch was requested; the switch aborts if another one serves before it switches.
   */
  from_deployment_id?: string;
  from_revision?: number;
  /**
   * The rollback target.
   */
  target_deployment_id: string;
  target_revision?: number;
  /**
   * Deployments whose release commands the restore undoes; rolling back to them is refused afterwards.
   */
  undone_deployment_ids: Array<string>;
  /**
   * The database the app leaves. It keeps every write.
   */
  source_database_id: string;
  source_database_name?: string;
  /**
   * The restored copy, once its restore has started.
   */
  target_database_id?: string;
  /**
   * Name of the restored copy.
   */
  target_name: string;
  /**
   * Writes after this moment stay only in the source database.
   */
  restore_point: string;
  drain: DatabaseSwitchDrain;
  /**
   * Why a switch failed or was cancelled, or why it is aborting.
   */
  failure_code?: string;
  failure_detail?: string;
  /**
   * The latest transient error the switch is retrying.
   */
  last_error_code?: string;
  requested_by?: string;
  cancel_requested_at?: string;
  fenced_at?: string;
  release_switched_at?: string;
  /**
   * When the bindings moved to the copy; there is no way back after it.
   */
  activated_at?: string;
  finished_at?: string;
  phase_started_at: string;
  created_at: string;
  updated_at: string;
};

