/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresExportManifest } from './ManagedPostgresExportManifest.js';
export type ManagedPostgresExport = {
  id: string;
  state: 'queued' | 'running' | 'ready' | 'failed';
  expires_at: string;
  manifest?: ManagedPostgresExportManifest;
};

