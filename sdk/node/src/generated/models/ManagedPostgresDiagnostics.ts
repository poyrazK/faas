/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresActivitySample } from './ManagedPostgresActivitySample.js';
export type ManagedPostgresDiagnostics = {
  database_id: string;
  state: 'available' | 'parked' | 'unavailable';
  sample?: ManagedPostgresActivitySample;
};

