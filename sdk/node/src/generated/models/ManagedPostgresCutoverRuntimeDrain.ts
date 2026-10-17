/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresCutoverRuntimeDrainMember } from './ManagedPostgresCutoverRuntimeDrainMember.js';
/**
 * Historical scheduler instance catalog evidence at checked_at. Catalog retirement does not prove complete writer isolation. No fence or drain is initiated by preflight. Absent on pending requests and reports created before runtime observations were supported.
 */
export type ManagedPostgresCutoverRuntimeDrain = {
  state: 'unavailable' | 'not_fenced' | 'missing' | 'draining' | 'blocked' | 'catalog_retired';
  /**
   * Captured catalog count; a lower bound when inventory_limit_exceeded is reported.
   */
  inventory_count: number;
  pending: number;
  /**
   * Members with matching native retirement receipts.
   */
  retired: number;
  blocked: number;
  /**
   * Plan and authority blocker codes. Member blockers appear on each member.
   */
  blockers: Array<string>;
  members: Array<ManagedPostgresCutoverRuntimeDrainMember>;
};

