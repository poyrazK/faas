/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MigrationDNSRecord } from './MigrationDNSRecord.js';
/**
 * Immutable production cutover plan. The stable ID reconciles lost responses. Preparation changes no traffic or domain bindings.
 */
export type CreateMigrationCutoverRequest = {
  id: string;
  verification_id: string;
  hostname: string;
  previous_records: Array<MigrationDNSRecord>;
  previous_proxy_enabled: boolean;
  max_5xx_rate_bps: number;
  max_p95_ms: number;
};

