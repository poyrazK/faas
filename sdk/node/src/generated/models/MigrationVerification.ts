/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateMigrationVerificationRequest } from './CreateMigrationVerificationRequest.js';
import type { MigrationVerificationCheck } from './MigrationVerificationCheck.js';
/**
 * Durable migration report tied to a deployment, configuration revision and optional cold-wake evidence.
 */
export type MigrationVerification = {
  id: string;
  app: string;
  deployment_id: string;
  scope: string;
  /**
   * Opaque fingerprint of deployment and scoped runtime configuration.
   */
  configuration_revision: string;
  status: 'pending' | 'passed' | 'failed' | 'blocked' | 'expired' | 'cancelled';
  phase: 'dependencies' | 'startup' | 'park' | 'drain' | 'wake' | 'waking' | 'cold_smoke' | 'cold_dependencies' | 'restoring' | 'complete';
  reason?: string;
  request: CreateMigrationVerificationRequest;
  checks: Array<MigrationVerificationCheck>;
  dependency_task_id?: string;
  cold_dependency_task_id?: string;
  wake_id?: string;
  instance_id?: string;
  created_at: string;
  expires_at: string;
  drained_at?: string;
  completed_at?: string;
  /**
   * Durable cancellation intent; pending status includes task shutdown and service recovery.
   */
  cancel_requested_at?: string;
  /**
   * Restoration outcome after interrupted verification. Restored requires a running instance; superseded leaves the new deployment alone; blocked requires resolving an account or security restriction.
   */
  recovery_status?: 'pending' | 'restored' | 'superseded' | 'blocked';
};

