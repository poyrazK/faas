/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectMigrationBaselineVerification } from './ProjectMigrationBaselineVerification.js';
import type { ProjectMigrationContract } from './ProjectMigrationContract.js';
/**
 * Planned backward-compatible migration identity and durable task outcome. Task logs remain on the app-task resource.
 */
export type ProjectReleaseCommand = {
  app_id: string;
  slug: string;
  command_hash: string;
  compatibility: 'backward-compatible';
  status: 'waiting' | 'queued' | 'restoring' | 'running' | 'succeeded' | 'failed' | 'timed_out' | 'cancelled' | 'uncertain';
  task_id?: string;
  exit_code?: number;
  failure_code?: string;
  /**
   * Require attributed production baseline probes after migration and before publication.
   */
  verify_baseline?: boolean;
  baseline_verification?: ProjectMigrationBaselineVerification;
  /**
   * Immutable hash of the source-declared cleanup command.
   */
  contract_command_hash?: string;
  contract?: ProjectMigrationContract;
};

