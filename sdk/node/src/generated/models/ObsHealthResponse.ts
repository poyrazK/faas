/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Wire shape for GET /v1/admin/obs/health (admin scope +
 * FAAS_ADMIN_EMAILS allowlist + MFA). Composed from
 * apid's own Prometheus counters (audit_log_write_total /
 * audit_log_write_failures_total / audit_log_coverage_ratio_5m),
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
   * Snapshot timestamp (UTC, RFC 3339).
   */
  generated_at: string;
  /**
   * True only when every PromQL query used for this snapshot
   * completed successfully. False means Prometheus-derived numeric
   * fields are fallback values and must not be interpreted as
   * observed telemetry.
   *
   */
  prometheus_available: boolean;
  /**
   * Sum of audit_log_write_total over the trailing 5m
   * window. 0 when the audit pipeline has been silent in the
   * window. Check `prometheus_available` before interpreting it.
   *
   */
  audit_log_write_total_5m: number;
  /**
   * Sum of audit_log_write_failures_total over the trailing
   * 5m window. Check `prometheus_available` before interpreting it.
   *
   */
  audit_log_write_failures_5m: number;
  /**
   * Ratio of audit_log writes with a non-NULL trace_id
   * over all audit_log writes in the window. 1.0
   * (vacuous truth) when the audit pipeline has been silent
   * in the window. Check `prometheus_available` before
   * interpreting it.
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
   * via PromQL ALERTS{alertstate="firing"}. Check
   * `prometheus_available` before interpreting a zero value.
   *
   */
  alerts_firing: number;
};

