/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresCutoverRuntimeDrain } from './ManagedPostgresCutoverRuntimeDrain.js';
import type { ManagedPostgresCutoverSourceSessions } from './ManagedPostgresCutoverSourceSessions.js';
/**
 * Historical catalog checks and remaining activation blockers. Never an activation token; sealed credentials and provider identities are private.
 */
export type ManagedPostgresCutoverPreflight = {
  cutover_id: string;
  state: 'pending' | 'blocked';
  revision: number;
  /**
   * Always false; customer activation remains disabled.
   */
  activation_allowed: boolean;
  /**
   * Recomputed on read for pending checks, expiry, future check time, cutover changes, cancellation or changed runtime authority and receipts. Older reports without runtime observations require a new check.
   */
  stale: boolean;
  /**
   * Affected source binding IDs observed during evaluation.
   */
  binding_ids: Array<string>;
  blockers: Array<'activation_disabled' | 'runtime_safety_unproven' | 'source_sessions_unfenced' | 'routes_unconverged' | 'snapshot_invalidation_unproven' | 'resume_plan_missing' | 'cutover_inactive' | 'source_changed' | 'target_changed' | 'verification_required' | 'bindings_changed'>;
  /**
   * All staged credentials had fresh SQL verification at checked_at.
   */
  verification_fresh: boolean;
  source_matches: boolean;
  target_matches: boolean;
  bindings_match: boolean;
  runtime_drain?: ManagedPostgresCutoverRuntimeDrain;
  source_sessions?: ManagedPostgresCutoverSourceSessions;
  observed_cutover_updated_at?: string;
  checked_at?: string;
  expires_at?: string;
  created_at: string;
  updated_at: string;
};

