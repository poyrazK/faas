/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresLiveKey } from './ManagedPostgresLiveKey.js';
/**
 * Live-query state of a managed database (ADR-1384). secret appears only in the response that issues it.
 */
export type ManagedPostgresLiveQueries = {
  database_id: string;
  enabled: boolean;
  keys: Array<ManagedPostgresLiveKey>;
  /**
   * The new live secret (glsk1_...). Store it as a backend secret; it cannot be read again.
   */
  secret?: string;
};

