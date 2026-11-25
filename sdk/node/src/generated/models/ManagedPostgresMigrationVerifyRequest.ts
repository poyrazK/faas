/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresImportSource } from './ManagedPostgresImportSource.js';
export type ManagedPostgresMigrationVerifyRequest = {
  operation_id: string;
  source: ManagedPostgresImportSource;
  /**
   * Reuse the UUID during the same continuous source write pause.
   */
  verification_id: string;
  /**
   * The operator confirms that source writers are paused and drained. Must be true.
   */
  source_writes_paused: boolean;
};

