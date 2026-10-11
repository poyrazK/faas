/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentQualificationCheck } from './ProjectEnvironmentQualificationCheck.js';
/**
 * Non-secret, 24-hour qualification receipt for an immutable release set and the exact source configuration and per-workload secret revision snapshots probed.
 */
export type ProjectEnvironmentQualificationResponse = {
  /**
   * Immutable per-workload fingerprints bound to this qualification receipt.
   */
  workload_config_hashes?: Record<string, string>;
  id: string;
  environment: string;
  release_set_id: string;
  /**
   * Version tested; -1 marks a legacy receipt that predates configuration binding and cannot qualify for promotion.
   */
  configuration_version: number;
  /**
   * SHA-256 of canonical non-secret environment configuration; empty only for a legacy receipt that cannot qualify for promotion.
   */
  configuration_hash: string;
  /**
   * Opaque per-workload fingerprints of secret keys, version metadata, and managed credential generations; no secret values or value hashes are included. Empty only for a legacy receipt that cannot qualify for promotion.
   */
  secret_revision_hashes: Record<string, string>;
  status: 'passed' | 'failed';
  checks: Array<ProjectEnvironmentQualificationCheck>;
  created_at: string;
  expires_at: string;
};

