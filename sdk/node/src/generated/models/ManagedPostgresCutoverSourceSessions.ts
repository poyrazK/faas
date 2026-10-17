/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresCutoverWriterIsolation } from './ManagedPostgresCutoverWriterIsolation.js';
/**
 * Historical source SQL admission and session observations for the frozen selected database set. Never complete writer-isolation proof. Preflight and GET initiate no SQL fences. Absent on pending and older reports. Inspect the parent stale and expires_at fields.
 */
export type ManagedPostgresCutoverSourceSessions = {
  state: 'unavailable' | 'not_requested' | 'pending' | 'closing' | 'closed' | 'abandoning' | 'released';
  /**
   * Admission closure was independently confirmed and fresh at checked_at.
   */
  admission_closed: boolean;
  /**
   * Selected databases had no observed sessions or prepared transactions at checked_at. Background writers and future catalog stability remain unproven.
   */
  selected_databases_drained: boolean;
  selected_database_count: number;
  sessions: number;
  prepared_transactions: number;
  unselected_databases: number;
  blockers: Array<string>;
  observed_at?: string;
  writer_isolation?: ManagedPostgresCutoverWriterIsolation;
};

