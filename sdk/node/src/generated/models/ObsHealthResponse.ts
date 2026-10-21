/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsAlertCollectionHealth } from './ObsAlertCollectionHealth.js';
import type { ObsAlertRetentionHealth } from './ObsAlertRetentionHealth.js';
import type { ObsHealthSignal } from './ObsHealthSignal.js';
/**
 * Wire shape for GET /v1/admin/obs/health (admin scope +
 * FAAS_ADMIN_EMAILS allowlist + MFA). Composed from
 * apid's own Prometheus counters (apid_audit_log_write_total /
 * apid_audit_log_write_failures_total), a SQL audit-log coverage ratio,
 * a single SQL aggregate over operator_intents (stuck-running
 * rows), a single SQL aggregate over events (trace_id
 * coverage), and a PromQL count of firing alerts. Kinds
 * with zero rows in the SQL-derived fields are seeded to
 * 0 (counts) or 1.0 (ratios, vacuous truth) so the JSON
 * shape stays stable on a fresh deploy.
 *
 */
export type ObsHealthResponse = {
  /**
   * Response generation timestamp (UTC, RFC 3339); not a telemetry observation time.
   */
  generated_at: string;
  /**
   * True only when all three Prometheus signals are healthy with
   * fresh underlying observations. When false, consult each signal:
   * healthy values remain usable and other numeric values are zero
   * compatibility fallbacks. On legacy responses without signals,
   * false makes all Prometheus-derived values unavailable.
   *
   */
  prometheus_available: boolean;
  /**
   * Increase of apid_audit_log_write_total over the trailing 5m
   * window. Zero activity is a healthy zero only when the matching
   * signals entry is healthy; otherwise zero is a compatibility fallback.
   *
   */
  audit_log_write_total_5m: number;
  /**
   * Increase of apid_audit_log_write_failures_total over the trailing
   * 5m window. Interpret only when the matching signals entry is
   * healthy, or prometheus_available is true on a legacy response.
   *
   */
  audit_log_write_failures_5m: number;
  /**
   * Ratio of durable audit_log rows with a valid W3C trace_id
   * in their JSON payload over all audit rows in the window. 1.0
   * (vacuous truth) when no durable audit rows exist in the window.
   * This ratio comes from SQL, independently of Prometheus availability.
   *
   */
  audit_log_coverage_ratio_5m: number;
  /**
   * Per-kind count of operator_intents rows stuck in
   * `running` past the 5m threshold. The handler seeds
   * every kind in the operator-action vocabulary
   * (force_park, force_cold_boot, force_restart) with
   * 0 so the JSON shape stays stable.
   *
   */
  operator_intent_outcome_missing_total: Record<string, number>;
  /**
   * Per-kind ratio of operator.action.* events with a
   * non-NULL trace_id over all operator.action.* events
   * in the window. Kinds with zero rows are seeded to
   * 1.0 (vacuous truth — see Store interface comment).
   * Reads events (live), NOT audit_log (FK-free
   * post-deletion copy) — ADR-091 §3.7.4.
   *
   */
  trace_id_completeness_ratio: Record<string, number>;
  /**
   * Count of Prometheus alert rules in the firing state
   * via PromQL ALERTS{alertstate="firing"}, excluding watchdog and
   * info-only rules. A fresh faas.rules.yml evaluation heartbeat is
   * required for the matching signal to be healthy, including when
   * no alerts fire. This describes source freshness, not alert severity.
   *
   */
  alerts_firing: number;
  alert_history?: ObsAlertCollectionHealth;
  alert_retention?: ObsAlertRetentionHealth;
  /**
   * Independent evidence for audit_log_write_total_5m,
   * audit_log_write_failures_5m, and alerts_firing. Present on new
   * servers; optional for compatibility with older responses.
   * Only healthy signal values are usable. SQL-derived fields remain
   * independent; a database read failure returns 503.
   *
   */
  signals?: Record<string, ObsHealthSignal>;
};

