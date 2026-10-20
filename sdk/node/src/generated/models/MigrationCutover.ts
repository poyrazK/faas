/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateMigrationCutoverRequest } from './CreateMigrationCutoverRequest.js';
import type { DNSRecordInstruction } from './DNSRecordInstruction.js';
import type { MigrationCutoverTraffic } from './MigrationCutoverTraffic.js';
import type { MigrationVerificationCheck } from './MigrationVerificationCheck.js';
/**
 * Saved plan, immutable recovery routing and bounded background cutover evidence. Ready means current DNS, trusted TLS and an exact-deployment gateway response passed; completion remains explicit.
 */
export type MigrationCutover = {
  id: string;
  app: string;
  deployment_id: string;
  configuration_revision: string;
  smoke_path: string;
  status: 'prepared' | 'checking' | 'ready' | 'attention' | 'completed' | 'cancelled' | 'restored' | 'blocked';
  reason?: string;
  request: CreateMigrationCutoverRequest;
  domain_connected: boolean;
  dns_records: Array<DNSRecordInstruction>;
  checks: Array<MigrationVerificationCheck>;
  traffic: MigrationCutoverTraffic;
  created_at: string;
  started_at?: string;
  check_deadline?: string;
  checked_at?: string;
  hostname_verified_at?: string;
  completed_at?: string;
  limited_traffic_acknowledged: boolean;
};

