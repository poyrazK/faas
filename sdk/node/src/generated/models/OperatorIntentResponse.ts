/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Wire shape for GET /v1/admin/operator-intents/{id}
 * (admin scope + FAAS_ADMIN_EMAILS allowlist; NO MFA —
 * mirrors getFireCronRequest). IDOR closure: 404 (not 403)
 * on wrong-owner so an admin cannot distinguish "wrong id"
 * from "wrong owner".
 *
 */
export type OperatorIntentResponse = {
  intent_id: string;
  kind: 'force_park' | 'force_cold_boot' | 'force_restart' | 'node_drain' | 'node_force_drain' | 'node_activate';
  status: 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled';
  /**
   * Instance UUID (force_park or force_restart), deployment UUID (force_cold_boot), or compute-node UUID (node lifecycle intents).
   */
  target_id: string;
  /**
   * Owning account. Omitted for fleet-level node lifecycle intents.
   */
  account_id?: string;
  /**
   * Admin account that requested the operation.
   */
  actor_id: string;
  /**
   * Bounded operator-supplied reason recorded with the intent.
   */
  reason: string;
  /**
   * Kind-specific preflight and desired-state metadata captured before dispatch.
   */
  metadata: Record<string, any>;
  requested_at: string;
  /**
   * Set when schedd claims the intent (pending → running).
   */
  started_at?: string;
  /**
   * Set on terminal status (succeeded/failed/cancelled).
   */
  finished_at?: string;
  /**
   * Bounded dispatch error message (1 KB cap) on failed status.
   */
  error?: string;
  /**
   * Populated for force_cold_boot and force_restart on succeeded status. Empty when no snapshots existed.
   */
  snap_ids_marked_stale?: Array<string>;
  /**
   * Obs-Meta + Trace-IDs Mega-PR / C4. OTel W3C 32-char
   * hex identifier shared with the inbound HTTP request
   * and the schedd dispatch context. NULL when the row
   * predates C4 or when the inbound request carried no
   * trace_id (e.g. cron-fired reaper paths). Joins
   * "this alert" ↔ "this operator action" ↔ "this
   * schedd dispatch" on one column.
   *
   */
  trace_id?: string | null;
};

