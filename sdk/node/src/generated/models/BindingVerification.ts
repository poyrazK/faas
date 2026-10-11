/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingVerificationCheck } from './BindingVerificationCheck.js';
/**
 * Sanitized durable evidence from the latest admitted service, PostgreSQL or object-storage task guest canary. Object-storage verification records bucket listing or explicitly requested permission-aware object operations and cleanup; inspect checks for coverage. Resident application adoption is not checked.
 */
export type BindingVerification = {
  /**
   * Exact admitted canary mode. Older evidence with no mode is listing-only and cannot satisfy write requirements.
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

