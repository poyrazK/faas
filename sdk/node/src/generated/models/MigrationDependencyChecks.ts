/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MigrationServiceConnectionCheck } from './MigrationServiceConnectionCheck.js';
/**
 * Existing scoped environment names; values are resolved only in the task guest.
 */
export type MigrationDependencyChecks = {
  /**
   * PostgreSQL connection string environment key for authenticated SELECT 1.
   */
  postgres_env_key?: string;
  /**
   * Prefix for the existing six S3 binding variables; read-only listing.
   */
  object_storage_prefix?: string;
  /**
   * Environment key containing an HTTPS external API URL for a bounded GET; redirects are rejected.
   */
  http_env_key?: string;
  service_connections?: Array<MigrationServiceConnectionCheck>;
};

