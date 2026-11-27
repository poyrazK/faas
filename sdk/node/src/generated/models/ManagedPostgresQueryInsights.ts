/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresQueryInsight } from './ManagedPostgresQueryInsight.js';
/**
 * Ranked statement statistics and the coverage of the observation window.
 */
export type ManagedPostgresQueryInsights = {
  database_id: string;
  enabled: boolean;
  since: string;
  last_sample_at?: string;
  observed_through?: string;
  observed_seconds: number;
  queries: Array<ManagedPostgresQueryInsight>;
};

