/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresImportSource } from './ManagedPostgresImportSource.js';
export type ManagedPostgresImportRequest = {
  /**
   * Caller-generated lowercase UUID reused to recover or retry an import.
   */
  operation_id: string;
  source: ManagedPostgresImportSource;
};

