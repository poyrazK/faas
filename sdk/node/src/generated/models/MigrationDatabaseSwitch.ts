/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DatabaseSwitchDrain } from './DatabaseSwitchDrain.js';
/**
 * A durable binding switch to a verified native PostgreSQL migration target. The app is fenced while its bindings change.
 */
export type MigrationDatabaseSwitch = {
  migration_operation_id: string;
  id: string;
  app_id: string;
  /**
   * The deployment scope whose database bindings change.
   */
  scope: string;
  /**
   * The app pauses during fencing and resumes after the bindings switch. If cancellation wins, the activation unwinds.
   */
  phase: 'restoring' | 'staging' | 'verifying' | 'fencing' | 'settling' | 'draining' | 'switching' | 'resuming' | 'aborting' | 'completed' | 'failed' | 'cancelled';
  /**
   * The release serving when the migration activation was requested.
   */
  from_deployment_id?: string;
  from_revision?: number;
  /**
   * The app release serving before and after the migration.
   */
  target_deployment_id: string;
  target_revision?: number;
  /**
   * Empty for a migration switch.
   */
  undone_deployment_ids: Array<string>;
  /**
   * The app stays on its current release.
   */
  keeps_release?: boolean;
  /**
   * The source database the app leaves.
   */
  source_database_id: string;
  source_database_name?: string;
  /**
   * The native target database.
   */
  target_database_id?: string;
  /**
   * Name of the native target database.
   */
  target_name: string;
  /**
   * Omitted for migration activations.
   */
  restore_point?: string;
  drain: DatabaseSwitchDrain;
  /**
   * Reason the migration activation failed, was cancelled, or is aborting.
   */
  failure_code?: string;
  failure_detail?: string;
  /**
   * The last transient error during migration activation.
   */
  last_error_code?: string;
  requested_by?: string;
  cancel_requested_at?: string;
  fenced_at?: string;
  release_switched_at?: string;
  /**
   * When the bindings moved to the native target.
   */
  activated_at?: string;
  finished_at?: string;
  phase_started_at: string;
  created_at: string;
  updated_at: string;
};

