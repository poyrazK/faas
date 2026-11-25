/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresExportManifest } from './ManagedPostgresExportManifest.js';
/**
 * Status of a portable PostgreSQL dump and its manifest when ready.
 */
export type ManagedPostgresExport = {
  id: string;
  state: 'queued' | 'running' | 'ready' | 'failed';
  expires_at: string;
  manifest?: ManagedPostgresExportManifest;
};

