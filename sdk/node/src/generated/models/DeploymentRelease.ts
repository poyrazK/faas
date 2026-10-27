/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ReleaseDatabaseRef } from './ReleaseDatabaseRef.js';
/**
 * A deployment's release command run.
 */
export type DeploymentRelease = {
  /**
   * The release task's status; pending before the image service has admitted the task.
   */
  status: 'pending' | 'queued' | 'restoring' | 'running' | 'succeeded' | 'failed' | 'timed_out' | 'cancelled';
  started_at?: string;
  finished_at?: string;
  /**
   * Managed PostgreSQL databases whose migration binding the release task receives.
   */
  migration_databases?: Array<ReleaseDatabaseRef>;
};

