/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresExtensionVersion } from './ManagedPostgresExtensionVersion.js';
/**
 * Signed snapshot metadata and integrity checks for a portable export.
 */
export type ManagedPostgresExportManifest = {
  postgres_major: number;
  captured_at: string;
  extensions: Array<ManagedPostgresExtensionVersion>;
  size_bytes: number;
  sha256: string;
  /**
   * Source database size used to estimate restore space.
   */
  database_bytes?: number;
  /**
   * The schema containing an installed vector extension.
   */
  vector_schema?: string;
  /**
   * Gregale's authentication of this archive and manifest for managed restore.
   */
  restore_receipt?: string;
};

