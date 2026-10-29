/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingVerificationCheck } from './BindingVerificationCheck.js';
import type { ManagedServiceProbeSpec } from './ManagedServiceProbeSpec.js';
/**
 * Sanitized durable evidence from the latest admitted service, PostgreSQL or object-storage task guest canary. Object-storage verification records bucket listing or explicitly requested permission-aware object operations and cleanup; inspect checks for coverage. Resident application adoption is not checked.
 */
export type BindingVerification = {
  managed_service?: ManagedServiceProbeSpec;
  /**
   * Original probe task identity for pending observation and durable receipts.
   */
  task_id?: string;
  /**
   * Service recipient qualified by the probe; absent for legacy any-replica evidence.
   */
  target_deployment_id?: string;
  /**
   * Object operation level admitted for this stored verification; missing values denote a legacy listing canary and do not qualify required write operations.
   */
  object_storage_mode?: 'read' | 'write' | 'read_write';
  /**
   * Outcome of the recorded canary: passed, failed or unknown. May describe stale evidence.
   */
  result: string;
  /**
   * Stable reason such as configuration_changed, deployment_changed, probe_pending, report_invalid or report_truncated.
   */
  reason?: string;
  /**
   * Currently task_guest.
   */
  source: string;
  deployment_id: string;
  scope: string;
  checked_at?: string;
  credential_generation?: number;
  checks?: Array<BindingVerificationCheck>;
};

