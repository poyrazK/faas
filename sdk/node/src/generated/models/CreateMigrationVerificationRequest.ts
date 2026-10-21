/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MigrationDependencyChecks } from './MigrationDependencyChecks.js';
/**
 * Immutable migration checks and explicit consent to park and wake the selected Gregale app.
 */
export type CreateMigrationVerificationRequest = {
  /**
   * Stable caller-generated identity for creation recovery.
   */
  id: string;
  deployment_id: string;
  checks: MigrationDependencyChecks;
  /**
   * Read-only GET path on this app; no query, fragment or absolute URL. A successful 2xx response is required.
   */
  smoke_path: string;
  /**
   * Explicitly park this Gregale app and verify a correlated wake followed by repeated checks.
   */
  cold_wake: boolean;
};

