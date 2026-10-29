/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectMigrationRecovery } from './ProjectMigrationRecovery.js';
/**
 * Guarded schema cleanup admission and durable task result. Cleanup command and task logs remain private to their existing resources.
 */
export type ProjectMigrationContract = {
  status: 'ready' | 'blocked' | 'queued' | 'restoring' | 'running' | 'succeeded' | 'failed' | 'timed_out' | 'cancelled' | 'uncertain' | 'unknown';
  reason?: string;
  request_id?: string;
  expected_generation?: number;
  task_id?: string;
  retired_deployments: number;
  exit_code?: number;
  failure_code?: string;
  recovery?: ProjectMigrationRecovery;
};

