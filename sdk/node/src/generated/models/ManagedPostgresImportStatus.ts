/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresExtensionVersion } from './ManagedPostgresExtensionVersion.js';
/**
 * Import progress or the results of a compatibility check. The source login is never returned.
 */
export type ManagedPostgresImportStatus = {
  state: 'checked' | 'running' | 'done' | 'failed' | 'unverified';
  operation_id?: string;
  error_code?: string;
  source_major?: number;
  started_at?: string;
  finished_at?: string;
  missing_extensions?: Array<string>;
  required_volume_bytes?: number;
  incompatible_extensions?: Array<ManagedPostgresExtensionVersion>;
};

